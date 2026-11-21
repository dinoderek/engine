package telegram

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

var operator = control.Actor{Name: "operator", Role: "operator"}

// Loop runs beside the coordinator, sharing its command service and store.
// mu serializes delivery edits with callback edits, never with coordinator work.
type Loop struct {
	Service   control.Service
	Bot       Bot
	Chat      int64
	Dashboard string
	Logf      func(string, ...any)
	mu        sync.Mutex
}

func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func retryDelay(err error, delay time.Duration) time.Duration {
	var api *apiError
	if errors.As(err, &api) {
		delay = max(delay, api.Retry)
	}
	return delay
}
func (l *Loop) log(err error) {
	if err != nil && l.Logf != nil {
		l.Logf("telegram: %v", err)
	}
}

// Run keeps delivery scans independent of long polling. Both back off on error.
func (l *Loop) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Go(func() { l.poll(ctx) })
	delay := 5 * time.Second
	for ctx.Err() == nil {
		err := l.Pass(ctx, time.Now())
		l.log(err)
		if err == nil {
			delay = 5 * time.Second
		} else {
			delay = retryDelay(err, min(delay*2, time.Minute))
		}
		if !wait(ctx, delay) {
			break
		}
	}
	wg.Wait()
}

func (l *Loop) inbox() (map[int]control.Update, []control.Update, error) {
	open := map[int]control.Update{}
	var all []control.Update
	after := 0
	for {
		out, err := l.Service.Execute(operator, control.Request{Op: "inbox", After: after})
		if err != nil {
			return nil, nil, err
		}
		for _, u := range out.Updates {
			open[u.ID] = u
			all = append(all, u)
			after = u.ID
		}
		if len(out.Updates) < 200 {
			return open, all, nil
		}
	}
}

// Pass stages new inbox entries durably before sending, retries pending sends,
// and edits settled deliveries. It never holds the store across a network call.
func (l *Loop) Pass(ctx context.Context, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	rows, _, lastDigest, err := l.Service.Store.TelegramState()
	if err != nil {
		return err
	}
	open, all, err := l.inbox()
	if err != nil {
		return err
	}
	rows, err = l.stage(rows, all)
	if err != nil {
		return err
	}
	settleErr := l.settle(ctx, rows, open)
	var infos []control.TelegramDelivery
	for _, d := range rows {
		if d.Settled || d.Message != 0 {
			continue
		}
		u, ok := open[d.Update]
		if !ok {
			continue
		}
		if !u.RequiresReply {
			d.Text = info(u)
			if digestFits(append(infos, d)) {
				infos = append(infos, d)
			}
			continue
		}
		if err = l.sendQuestion(ctx, u, d); err != nil {
			return errors.Join(settleErr, err)
		}
	}
	if len(infos) == 0 || now.Sub(lastDigest) < time.Minute {
		return settleErr
	}
	message, err := l.send(ctx, digest(infos), keyboard{Rows: [][]button{}}, true)
	if err != nil {
		return errors.Join(settleErr, err)
	}
	for i := range infos {
		infos[i].Message = message
	}
	return errors.Join(settleErr, l.Service.Store.SaveTelegram(infos, 0, now))
}

func (l *Loop) send(ctx context.Context, text string, keys keyboard, silent bool) (int64, error) {
	var msg Message
	err := l.Bot.call(ctx, "sendMessage", map[string]any{"chat_id": l.Chat, "text": text, "reply_markup": keys, "disable_notification": silent}, &msg)
	if err == nil && msg.ID == 0 {
		err = errors.New("telegram send returned no message ID")
	}
	return msg.ID, err
}
func (l *Loop) edit(ctx context.Context, message int64, text string) error {
	err := l.Bot.call(ctx, "editMessageText", map[string]any{"chat_id": l.Chat, "message_id": message, "text": text, "reply_markup": keyboard{Rows: [][]button{}}}, nil)
	var api *apiError
	if errors.As(err, &api) && api.Unchanged {
		return nil
	}
	return err
}

func (l *Loop) settle(ctx context.Context, rows []control.TelegramDelivery, open map[int]control.Update) error {
	groups := map[int64][]control.TelegramDelivery{}
	changed := map[int64]bool{}
	for _, d := range rows {
		if !d.Settled {
			if _, ok := open[d.Update]; !ok {
				d.Settled = true
				changed[d.Message] = true
			}
		}
		groups[d.Message] = append(groups[d.Message], d)
	}
	var failures []error
	for message, group := range groups {
		if changed[message] {
			if err := l.settleGroup(ctx, message, group); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func (l *Loop) settleGroup(ctx context.Context, message int64, group []control.TelegramDelivery) error {
	if message != 0 {
		text, err := l.settlementText(group)
		if err != nil {
			return err
		}
		if err = l.edit(ctx, message, text); err != nil {
			return err
		}
	}
	return l.Service.Store.SaveTelegram(group, 0, time.Time{})
}

func (l *Loop) answer(question control.Update) (string, error) {
	after := question.ID
	for {
		out, err := l.Service.Execute(operator, control.Request{Op: "updates", ID: question.Item, After: after})
		if err != nil {
			return "", err
		}
		for _, u := range out.Updates {
			after = u.ID
			if u.ReplyTo == question.ID && (u.Kind == "resolved" || u.Kind == "pane-answer") {
				return "Answered: " + clip(u.Body, 1000), nil
			}
		}
		if len(out.Updates) < 200 {
			return "Already answered", nil
		}
	}
}

func (l *Loop) poll(ctx context.Context) {
	delay := time.Second
	for ctx.Err() == nil {
		_, offset, _, err := l.Service.Store.TelegramState()
		if err == nil {
			var updates []Incoming
			updates, err = l.Bot.Updates(ctx, offset, 20)
			for _, u := range updates {
				if err != nil {
					break
				}
				if u.Callback != nil {
					err = l.tap(ctx, *u.Callback)
				}
				if err == nil {
					err = l.Service.Store.SaveTelegram(nil, u.ID+1, time.Time{})
				}
			}
		}
		if err == nil {
			delay = time.Second
		} else {
			l.log(err)
			delay = retryDelay(err, min(delay*2, time.Minute))
		}
		if !wait(ctx, delay) {
			return
		}
	}
}

func (l *Loop) tap(ctx context.Context, c Callback) error {
	if c.Message == nil || c.Message.Chat.ID != l.Chat {
		return nil
	}
	id, index, ok := parseCallback(c.Data)
	if !ok || c.ID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rows, _, _, err := l.Service.Store.TelegramState()
	if err != nil {
		return err
	}
	var delivery *control.TelegramDelivery
	for _, d := range rows {
		if d.Update == id && d.Message == c.Message.ID && d.Message != 0 {
			copy := d
			delivery = &copy
			break
		}
	}
	if delivery == nil {
		return nil
	}
	out, err := l.Service.Execute(operator, control.Request{Op: "updates", After: id - 1})
	if err != nil {
		return err
	}
	if len(out.Updates) == 0 || out.Updates[0].ID != id {
		return nil
	}
	q := out.Updates[0]
	if !q.RequiresReply || index >= len(q.Choices) {
		return nil
	}
	_, err = l.Service.Execute(operator, control.Request{Op: "resolve", ReplyTo: id, Body: q.Choices[index], Key: fmt.Sprintf("telegram:%x", sha256.Sum256([]byte(c.ID)))})
	status := "Answered: " + q.Choices[index]
	if err != nil {
		var p *control.Problem
		if !errors.As(err, &p) || p.Status != 409 || p.Reason != "already-resolved" {
			return err
		}
		status = "Already answered"
	}
	delivery.Status = status
	delivery.Settled = false
	// Persist the presentation intent after the resolution, before a network
	// edit. A failed edit must not hold up acknowledgement of this callback.
	if err = l.Service.Store.SaveTelegram([]control.TelegramDelivery{*delivery}, 0, time.Time{}); err != nil {
		return err
	}
	if err = l.edit(ctx, delivery.Message, delivery.Text+"\n\n"+status); err == nil {
		delivery.Settled = true
		err = l.Service.Store.SaveTelegram([]control.TelegramDelivery{*delivery}, 0, time.Time{})
	}
	l.log(err)
	l.log(l.Bot.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": c.ID, "text": status}, nil))
	return nil
}

func (l *Loop) stage(rows []control.TelegramDelivery, all []control.Update) ([]control.TelegramDelivery, error) {
	cursor := 0
	for _, d := range rows {
		cursor = max(cursor, d.Update)
	}
	var staged []control.TelegramDelivery
	for _, u := range all {
		if u.ID > cursor {
			staged = append(staged, control.TelegramDelivery{Update: u.ID})
		}
	}
	if len(staged) > 0 {
		if err := l.Service.Store.SaveTelegram(staged, 0, time.Time{}); err != nil {
			return nil, err
		}
	}
	return append(rows, staged...), nil
}

func (l *Loop) sendQuestion(ctx context.Context, u control.Update, d control.TelegramDelivery) error {
	text, keys := question(u, l.Dashboard)
	message, err := l.send(ctx, text, keys, false)
	if err != nil {
		return err
	}
	d.Message, d.Text = message, text
	return l.Service.Store.SaveTelegram([]control.TelegramDelivery{d}, 0, time.Time{})
}

func (l *Loop) settlementText(group []control.TelegramDelivery) (string, error) {
	if group[0].Status != "" {
		return group[0].Text + "\n\n" + group[0].Status, nil
	}
	// The immutable question remains readable after leaving the inbox.
	out, err := l.Service.Execute(operator, control.Request{Op: "updates", After: group[0].Update - 1})
	if err != nil {
		return "", err
	}
	if len(out.Updates) == 0 || out.Updates[0].ID != group[0].Update || !out.Updates[0].RequiresReply {
		return digest(group), nil
	}
	answer, err := l.answer(out.Updates[0])
	return group[0].Text + "\n\n" + answer, err
}
