package control

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// change is one record a transaction wrote or deleted.
type change struct {
	key, data string
	del       bool
}

func updateKey(id int) string { return fmt.Sprintf("update/%012d", id) }

// differ collects the records that differ between two states.
type differ struct {
	changes []change
	err     error
}

func (d *differ) put(key string, v any) {
	b, err := json.Marshal(v)
	if err != nil && d.err == nil {
		d.err = fmt.Errorf("control record %s: %w", key, err)
	}
	d.changes = append(d.changes, change{key: key, data: string(b)})
}

// changed puts cur unless it encodes as old does: values can differ in
// memory and still be one record.
func (d *differ) changed(key string, old, cur any) {
	was, _ := json.Marshal(old)
	d.put(key, cur)
	if last := len(d.changes) - 1; d.changes[last].data == string(was) {
		d.changes = d.changes[:last]
	}
}

func diffMap[V any](d *differ, prefix string, old, cur map[string]V) {
	for k, v := range cur {
		if o, ok := old[k]; !ok {
			d.put(prefix+k, v)
		} else if !reflect.DeepEqual(o, v) {
			d.changed(prefix+k, o, v)
		}
	}
	for k := range old {
		if _, ok := cur[k]; !ok {
			d.changes = append(d.changes, change{key: prefix + k, del: true})
		}
	}
}

// updates compares history. Updates are appended and now and then replaced,
// so cur normally starts with old's; otherwise every update is rewritten.
func (d *differ) updates(old, cur []Update) {
	aligned := len(cur) >= len(old)
	for i := 0; aligned && i < len(old); i++ {
		aligned = cur[i].ID == old[i].ID
	}
	if !aligned {
		kept := map[int]bool{}
		for _, u := range cur {
			kept[u.ID] = true
		}
		for _, u := range old {
			if !kept[u.ID] {
				d.changes = append(d.changes, change{key: updateKey(u.ID), del: true})
			}
		}
		old = nil
	}
	for i, u := range cur {
		if i >= len(old) {
			d.put(updateKey(u.ID), u)
		} else if !reflect.DeepEqual(old[i], u) {
			d.changed(updateKey(u.ID), old[i], u)
		}
	}
}

// diff lists the records to write to turn old into cur. Its cost follows the
// number of records, not their size: equal records share their strings.
func diff(old, cur *State) ([]change, error) {
	d := &differ{}
	if old.Next != cur.Next {
		d.put("next", cur.Next)
	}
	if !old.PassAt.Equal(cur.PassAt) {
		d.put("pass", cur.PassAt)
	}
	if !old.Upgrade.Start.Equal(cur.Upgrade.Start) || !old.Upgrade.End.Equal(cur.Upgrade.End) {
		d.put("upgrade", cur.Upgrade)
	}
	diffMap(d, "item/", old.Items, cur.Items)
	diffMap(d, "attempt/", old.Attempts, cur.Attempts)
	d.updates(old.Updates, cur.Updates)
	diffMap(d, "queue/", old.Queue, cur.Queue)
	diffMap(d, "auth/", old.Auth, cur.Auth)
	diffMap(d, "receipt/", old.Receipts, cur.Receipts)
	diffMap(d, "host/", old.Hosts, cur.Hosts)
	return d.changes, d.err
}

// decode reads the written records back, as a later open would.
func decode(changes []change) (State, error) {
	patch := emptyState()
	for _, c := range changes {
		if c.del {
			continue
		}
		if err := loadRecord(&patch, c.key, []byte(c.data)); err != nil {
			return patch, fmt.Errorf("control record %s: %w", c.key, err)
		}
	}
	return patch, nil
}

// install applies committed changes, with patch as their decoded records.
func (s *State) install(changes []change, patch State) {
	for _, c := range changes {
		kind, id, _ := strings.Cut(c.key, "/")
		switch {
		case kind == "next":
			s.Next = patch.Next
		case kind == "pass":
			s.PassAt = patch.PassAt
		case kind == "upgrade":
			s.Upgrade = patch.Upgrade
		case !c.del:
		case kind == "update":
			n, _ := strconv.Atoi(id)
			if i, ok := s.find(n); ok {
				s.Updates = slices.Delete(s.Updates, i, i+1)
			}
		default:
			s.drop(kind, id)
		}
	}
	maps.Copy(s.Items, patch.Items)
	maps.Copy(s.Attempts, patch.Attempts)
	maps.Copy(s.Queue, patch.Queue)
	maps.Copy(s.Auth, patch.Auth)
	maps.Copy(s.Receipts, patch.Receipts)
	maps.Copy(s.Hosts, patch.Hosts)
	for _, u := range patch.Updates {
		if i, ok := s.find(u.ID); ok {
			s.Updates[i] = u
		} else {
			s.Updates = slices.Insert(s.Updates, i, u)
		}
	}
}

// find is the place of update id in history, which a load orders by id.
func (s *State) find(id int) (int, bool) {
	return slices.BinarySearchFunc(s.Updates, id, func(u Update, id int) int { return u.ID - id })
}

func (s *State) drop(kind, id string) {
	switch kind {
	case "item":
		delete(s.Items, id)
	case "attempt":
		delete(s.Attempts, id)
	case "queue":
		delete(s.Queue, id)
	case "auth":
		delete(s.Auth, id)
	case "receipt":
		delete(s.Receipts, id)
	case "host":
		delete(s.Hosts, id)
	}
}

// load reads every record. Version 1's lock records are returned apart,
// for the migration; a version 2 database has none.
func (s *Store) load() (State, []legacyLock, error) {
	st := emptyState()
	out, err := s.sql("SELECT key,data FROM records ORDER BY key;")
	if err != nil {
		return st, nil, err
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return st, nil, nil
	}
	var rows []struct {
		Key  string
		Data string
	}
	if err = json.Unmarshal(out, &rows); err != nil {
		return st, nil, err
	}
	var locks []legacyLock
	for _, row := range rows {
		if strings.HasPrefix(row.Key, "lock/") {
			var l legacyLock
			if err = json.Unmarshal([]byte(row.Data), &l); err != nil {
				return st, nil, fmt.Errorf("control record %s: %w", row.Key, err)
			}
			locks = append(locks, l)
			continue
		}
		if err = loadRecord(&st, row.Key, []byte(row.Data)); err != nil {
			return st, nil, fmt.Errorf("control record %s: %w", row.Key, err)
		}
	}
	return st, locks, nil
}

func loadRecord(s *State, key string, b []byte) error {
	kind, id, _ := strings.Cut(key, "/")
	switch kind {
	case "next":
		return json.Unmarshal(b, &s.Next)
	case "pass":
		return json.Unmarshal(b, &s.PassAt)
	case "upgrade":
		return json.Unmarshal(b, &s.Upgrade)
	case "item":
		var v Item
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Items[id] = v
	case "attempt":
		var v Attempt
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Attempts[id] = v
	case "update":
		var v Update
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Updates = append(s.Updates, v)
	case "queue":
		var v QueueEntry
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Queue[id] = v
	case "auth":
		var v Actor
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Auth[id] = v
	case "receipt":
		var v receipt
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Receipts[id] = v
	case "host":
		var v HostRecord
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.Hosts[id] = v
	default:
		return fmt.Errorf("unknown record kind %q", kind)
	}
	return nil
}
