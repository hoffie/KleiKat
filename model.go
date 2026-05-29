package main


type Entry struct {
	Schema  string `json:"schema"`
	EntryID string `json:"entry_id"`
	Attrs   map[string]string `json:"attrs"`
}
