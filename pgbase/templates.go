package pgbase

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"sync"
)

// TemplateRegistry stores precompiled trusted templates shared by Go and
// JavaScript extensions. Registration replaces one name atomically.
type TemplateRegistry struct {
	mu      sync.RWMutex
	entries map[string]*template.Template
}

func (r *TemplateRegistry) Register(name, source string) error {
	if r == nil || name == "" {
		return errors.New("template registry and name are required")
	}
	compiled, err := template.New(name).Option("missingkey=error").Parse(source)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[string]*template.Template{}
	}
	r.entries[name] = compiled
	return nil
}

func (r *TemplateRegistry) Render(name string, data any) (string, error) {
	if r == nil {
		return "", errors.New("template registry is unavailable")
	}
	r.mu.RLock()
	compiled := r.entries[name]
	r.mu.RUnlock()
	if compiled == nil {
		return "", errors.New("template is not registered")
	}
	var output bytes.Buffer
	if err := compiled.Execute(&output, data); err != nil {
		return "", err
	}
	return output.String(), nil
}

func (r *TemplateRegistry) renderJSON(name string, raw json.RawMessage) (string, error) {
	var data any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil {
		return "", err
	}
	return r.Render(name, data)
}
