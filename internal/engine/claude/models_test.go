package claude

import (
	"reflect"
	"testing"

	"github.com/daffirds/bailey-cc-bridge/internal/engine"
)

func TestParseModels(t *testing.T) {
	line := []byte(`{"type":"control_response","response":{"subtype":"success","request_id":"models","response":{"models":[` +
		`{"value":"default","displayName":"Default (recommended)","description":"Opus · everyday","supportsEffort":true,"supportedEffortLevels":["low","high"]},` +
		`{"value":"haiku","displayName":"Haiku","description":"Fastest"}]}}}`)
	got, ok, err := parseModels(line)
	if !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	want := []engine.Model{
		{ID: "", Name: "Default (recommended)", Description: "Opus · everyday", Efforts: []string{"low", "high"}},
		{ID: "haiku", Name: "Haiku", Description: "Fastest"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	if _, ok, _ := parseModels([]byte(`{"type":"system","subtype":"init"}`)); ok {
		t.Error("took another line for the answer")
	}
}
