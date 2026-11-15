package config

import "testing"

// A profile narrows: deny wins over allow, a pattern with "/" matches
// server/command, one without it matches the command like a permissions rule.
func TestProfileAllows(t *testing.T) {
	reader := &Profile{Deny: []string{"*/*_write", "drop_*"}, Allow: []string{"*"}}
	cases := []struct {
		server, command string
		want            bool
	}{
		{"fs", "read_file", true},
		{"fs", "file_write", false},
		{"db", "drop_table", false},
	}
	for _, c := range cases {
		if got := reader.Allows(c.server, c.command); got != c.want {
			t.Errorf("Allows(%s, %s) = %v, want %v", c.server, c.command, got, c.want)
		}
	}
	// allow без deny — белый список: всё, что не названо, запрещено.
	onlyRead := &Profile{Allow: []string{"read_*"}}
	if !onlyRead.Allows("fs", "read_file") || onlyRead.Allows("fs", "write_file") {
		t.Fatalf("allow как белый список не работает: %+v", onlyRead)
	}
	// nil и пустой профиль ничего не запрещают.
	var none *Profile
	if !none.Allows("fs", "write_file") || !(&Profile{}).Allows("fs", "write_file") {
		t.Fatal("пустой профиль что-то запретил")
	}
}

func TestParseProfilesRejectsGarbage(t *testing.T) {
	bad := []string{
		`{"reader": {"denny": ["*"]}}`,
		`{"reader": {"deny": [""]}}`,
		`{"reader": "deny"}`,
		`{"": {"deny": ["*"]}}`,
		`"reader"`,
	}
	for _, raw := range bad {
		if _, err := ParseProfiles([]byte(raw)); err == nil {
			t.Errorf("принят мусор: %s", raw)
		}
	}
	// Имя нормализуется по краям: ключ выдаётся с TrimSpace, значит и профиль
	// должен называться так же.
	profiles, err := ParseProfiles([]byte(`{" reader ": {"deny": ["*"]}}`))
	if err != nil {
		t.Fatalf("имя с пробелами: %v", err)
	}
	if _, ok := profiles["reader"]; !ok || len(profiles) != 1 {
		t.Fatalf("имя не обрезано: %+v", profiles)
	}
	// Два имени, слипшихся после обрезки, — ошибка, а не тихий выбор.
	if _, err := ParseProfiles([]byte(`{"reader": {"deny": ["*"]}, " reader ": {"allow": ["*"]}}`)); err == nil {
		t.Fatal("дубль после обрезки принят")
	}

	profiles, err = ParseProfiles([]byte(`{"reader": {"deny": ["*/*_write"], "allow": ["*"]}}`))
	if err != nil {
		t.Fatalf("годный блок: %v", err)
	}
	if p := profiles["reader"]; p == nil || len(p.Deny) != 1 || len(p.Allow) != 1 {
		t.Fatalf("профиль не разобран: %+v", profiles)
	}
}
