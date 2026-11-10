package mcpserver

import "testing"

// namesOnly отдаёт имена, а не каталог: 440 тулов arr-status весили ~90 КБ.
func TestFilterCommandsNamesOnly(t *testing.T) {
	cmds := []commandInfo{
		{Name: "get_torrent_list", Description: "long description"},
		{Name: "add_torrent", Description: "long description"},
	}
	got, ok := filterCommands(cmds, "get_", true).([]string)
	if !ok || len(got) != 1 || got[0] != "get_torrent_list" {
		t.Fatalf("namesOnly: %#v", got)
	}
	full, ok := filterCommands(cmds, "", false).([]commandInfo)
	if !ok || len(full) != 2 || full[1].Description == "" {
		t.Fatalf("полный каталог: %#v", full)
	}
}
