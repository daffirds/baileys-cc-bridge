package codex

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/daffirds/baileys-cc-bridge/internal/engine"
)

// Models asks codex which models it offers (model/list on the app-server). No thread is started.
func (Engine) Models(ctx context.Context, bin string) ([]engine.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	s, err := launch(ctx, engine.Spec{Binary: bin, Workdir: os.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var pages []json.RawMessage
	params := map[string]any{}
	for {
		var page json.RawMessage
		if err := s.rpc.call(ctx, "model/list", params, &page); err != nil {
			return nil, err
		}
		pages = append(pages, page)
		var next struct {
			NextCursor string `json:"nextCursor"`
		}
		if json.Unmarshal(page, &next) != nil || next.NextCursor == "" {
			break
		}
		params = map[string]any{"cursor": next.NextCursor}
	}
	return parseModels(pages...)
}

// parseModels turns model/list pages into models, the default one first again as ID "".
func parseModels(pages ...json.RawMessage) ([]engine.Model, error) {
	var models []engine.Model
	var def *engine.Model
	for _, p := range pages {
		var page struct {
			Data []struct {
				ID                        string `json:"id"`
				Model                     string `json:"model"`
				DisplayName               string `json:"displayName"`
				Description               string `json:"description"`
				Hidden                    bool   `json:"hidden"`
				SupportedReasoningEfforts []struct {
					ReasoningEffort string `json:"reasoningEffort"`
				} `json:"supportedReasoningEfforts"`
				DefaultReasoningEffort string `json:"defaultReasoningEffort"`
				IsDefault              bool   `json:"isDefault"`
			} `json:"data"`
		}
		if err := json.Unmarshal(p, &page); err != nil {
			return nil, err
		}
		for _, c := range page.Data {
			if c.Hidden {
				continue
			}
			m := engine.Model{ID: firstNonEmpty(c.Model, c.ID), Name: firstNonEmpty(c.DisplayName, c.Model, c.ID),
				Description: c.Description, DefaultEffort: c.DefaultReasoningEffort}
			for _, e := range c.SupportedReasoningEfforts {
				m.Efforts = append(m.Efforts, e.ReasoningEffort)
			}
			models = append(models, m)
			if c.IsDefault {
				d := m
				def = &d
			}
		}
	}
	if def != nil {
		d := *def
		d.ID, d.Name, d.Description = "", "Default ("+def.Name+")", def.Description
		models = append([]engine.Model{d}, models...)
	}
	return models, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
