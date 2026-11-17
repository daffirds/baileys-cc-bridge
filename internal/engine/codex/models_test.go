package codex

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/daffirds/bailey-cc-bridge/internal/engine"
)

func TestParseModels(t *testing.T) {
	page := json.RawMessage(`{"data":[` +
		`{"id":"gpt-a","model":"gpt-a","displayName":"GPT-A","description":"workhorse","hidden":false,` +
		`"supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"high"}],"defaultReasoningEffort":"low","isDefault":true},` +
		`{"id":"gpt-old","model":"gpt-old","displayName":"Old","hidden":true,"isDefault":false},` +
		`{"id":"gpt-b","model":"gpt-b","displayName":"GPT-B","description":"frontier","isDefault":false}],"nextCursor":null}`)
	got, err := parseModels(page)
	if err != nil {
		t.Fatal(err)
	}
	a := engine.Model{ID: "gpt-a", Name: "GPT-A", Description: "workhorse", Efforts: []string{"low", "high"}, DefaultEffort: "low"}
	def := a
	def.ID, def.Name = "", "Default (GPT-A)"
	want := []engine.Model{def, a, {ID: "gpt-b", Name: "GPT-B", Description: "frontier"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}
