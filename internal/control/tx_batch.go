package control

import (
	"database/sql"
	"encoding/json"
	"strings"
	"unicode"
)

// Each accessor here answers, in one query, what a read would otherwise ask
// once per item: the set is passed as a JSON array, so the statement is the
// same whatever its size.

// idList is ids as the JSON array a query unpacks with json_each.
func idList[T string | int](ids []T) string {
	b, _ := json.Marshal(ids)
	return string(b)
}

// ItemsByID are the items with these IDs, keyed by ID. An unknown ID is
// absent.
func (t *Tx) ItemsByID(ids []string) map[string]Item {
	out := map[string]Item{}
	if len(ids) == 0 {
		return out
	}
	for _, it := range fetchAll[Item](t, "SELECT data FROM items WHERE id IN (SELECT value FROM json_each(?))", idList(ids)) {
		out[it.ID] = it
	}
	return out
}

// UpdatesByID are the updates with these sequences, keyed by sequence.
func (t *Tx) UpdatesByID(ids []int) map[int]Update {
	out := map[int]Update{}
	if len(ids) == 0 {
		return out
	}
	for _, u := range fetchAll[Update](t, "SELECT data FROM updates WHERE id IN (SELECT value FROM json_each(?))", idList(ids)) {
		out[u.ID] = u
	}
	return out
}

// TaskStatesOfPlans are TaskStatesOfPlan of each of plans, keyed by plan. A
// plan with no task is absent.
func (t *Tx) TaskStatesOfPlans(plans []string) map[string][]Item {
	out := map[string][]Item{}
	if len(plans) == 0 {
		return out
	}
	t.rows(func(rows *sql.Rows) error {
		var plan, state string
		if err := rows.Scan(&plan, &state); err != nil {
			return err
		}
		out[plan] = append(out[plan], Item{State: state})
		return nil
	}, "SELECT i.plan, i.state FROM json_each(?) j CROSS JOIN items i ON i.plan=j.value AND i.kind='task'", idList(plans))
	return out
}

// OwnQuestions are OwnQuestion of each of ids, keyed by ID: the first open
// question asked on the item, or by it on its plan. An item with none is
// absent.
func (t *Tx) OwnQuestions(ids []string) map[string]int {
	out := map[string]int{}
	if len(ids) == 0 {
		return out
	}
	open := " AND q.requires_reply=1 AND NOT EXISTS (SELECT 1 FROM updates r WHERE r.reply_to=q.id AND r.kind IN (" + resolvedKinds + "))"
	t.rows(func(rows *sql.Rows) error {
		var id string
		var question int
		if err := rows.Scan(&id, &question); err != nil {
			return err
		}
		out[id] = question
		return nil
	}, "SELECT k, MIN(question) FROM ("+
		"SELECT j.value AS k, q.id AS question FROM json_each(?1) j JOIN updates q ON q.item=j.value"+open+
		" UNION ALL SELECT j.value, q.id FROM json_each(?1) j JOIN updates q ON q.task=j.value"+open+
		") GROUP BY k", idList(ids))
	return out
}

// spaceChars is the characters strings.TrimSpace trims, as a SQL string.
var spaceChars = func() string {
	var b strings.Builder
	for r := rune(0); r <= 0x3000; r++ {
		if unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return "'" + b.String() + "'"
}()

// LatestMessages are, for each of ids, the newest update about the item that
// a person reads as a message: one of kinds, not the coordinator's, with a
// body that is not blank. An item with none is absent.
func (t *Tx) LatestMessages(ids []string, kinds []string) map[string]Update {
	out := map[string]Update{}
	if len(ids) == 0 {
		return out
	}
	t.rows(func(rows *sql.Rows) error {
		var data string
		if err := rows.Scan(&data); err != nil {
			return err
		}
		var u Update
		if err := json.Unmarshal([]byte(data), &u); err != nil {
			return err
		}
		out[u.Item] = u
		return nil
	}, "SELECT data FROM (SELECT (SELECT u.data FROM updates u WHERE u.item=j.value AND u.author!='coordinator'"+
		" AND u.kind IN (SELECT value FROM json_each(?2)) AND trim(json_extract(u.data, '$.body'), "+spaceChars+")!=''"+
		" ORDER BY u.id DESC LIMIT 1) AS data FROM json_each(?1) j) WHERE data IS NOT NULL", idList(ids), idList(kinds))
	return out
}
