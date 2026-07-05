package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/MunifTanjim/argus/internal/adapter/hookset"
)

// DefaultHookEvents are the Codex hook events argus registers. Codex has no
// Notification/SessionEnd events.
var DefaultHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"PermissionRequest",
	"Stop",
}

// hookTimeout returns the per-event command timeout in seconds.
func hookTimeout(event string) int {
	if event == "PermissionRequest" {
		return 1500
	}
	return 5
}

func managedCommand(argusBin, event string) string {
	return hookset.ManagedCommand(argusBin, Agent, event)
}

type hookCmd = hookset.Cmd
type hookGroup = hookset.Group

var predicate = hookset.Spec{Marker: hookset.ManagedMarker}

func isManaged(c hookCmd) bool           { return predicate.IsManaged(c) }
func hasManaged(groups []hookGroup) bool { return predicate.HasManaged(groups) }
func anyManaged(hooks hookset.Map) bool  { return predicate.AnyManaged(hooks) }

func codexHome() (string, error) {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func hooksJSONPath() (string, error) {
	dir, err := codexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hooks.json"), nil
}

func configTOMLPath() (string, error) {
	dir, err := codexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// SettingsPath returns the path of the file argus writes its hooks into.
func SettingsPath() (string, error) {
	s, err := activeStore()
	if err != nil {
		return "", err
	}
	return s.path, nil
}

// store is one on-disk hook backend. load returns os.IsNotExist when the file
// is absent.
type store struct {
	path string
	load func() (hookset.Map, error)
	save func(hookset.Map) error
}

func specFor(s store) hookset.Spec {
	return hookset.Spec{
		Marker:        hookset.ManagedMarker,
		Command:       managedCommand,
		Timeout:       hookTimeout,
		DefaultEvents: DefaultHookEvents,
		Load:          s.load,
		Save:          s.save,
	}
}

// activeStore picks where a fresh install goes: config.toml if it has hooks,
// else hooks.json.
func activeStore() (store, error) {
	has, err := configHasHooks()
	if err != nil {
		return store{}, err
	}
	if has {
		return tomlStore()
	}
	return jsonStore()
}

// stores returns both backends for operations that span both files.
func stores() ([]store, error) {
	ts, err := tomlStore()
	if err != nil {
		return nil, err
	}
	js, err := jsonStore()
	if err != nil {
		return nil, err
	}
	return []store{ts, js}, nil
}

// Install adds argus's hooks for the given events. Idempotent.
func Install(argusBin string, events []string) error {
	s, err := activeStore()
	if err != nil {
		return err
	}
	return specFor(s).Install(argusBin, events)
}

// ReconcileIfInstalled ensures managed hooks match DefaultHookEvents, if already
// installed.
func ReconcileIfInstalled(argusBin string) (added []string, err error) {
	all, err := stores()
	if err != nil {
		return nil, err
	}
	for _, s := range all {
		hooks, err := s.load()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if anyManaged(hooks) {
			return specFor(s).Reconcile(argusBin)
		}
	}
	return nil, nil
}

// Uninstall removes all argus-managed hooks from both stores.
func Uninstall() error {
	all, err := stores()
	if err != nil {
		return err
	}
	for _, s := range all {
		if err := specFor(s).Uninstall(); err != nil {
			return err
		}
	}
	return nil
}

// --- hooks.json backend ---

func jsonStore() (store, error) {
	path, err := hooksJSONPath()
	if err != nil {
		return store{}, err
	}
	return store{
		path: path,
		load: func() (hookset.Map, error) {
			top, err := readJSONTop(path)
			if err != nil {
				return nil, err
			}
			return decodeJSONHooks(top)
		},
		save: func(hooks hookset.Map) error { return saveJSONHooks(path, hooks) },
	}, nil
}

// readJSONTop reads hooks.json into a raw map. Missing file returns os.IsNotExist;
// empty file yields an empty map.
func readJSONTop(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	top := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return top, nil
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return top, nil
}

func decodeJSONHooks(top map[string]json.RawMessage) (hookset.Map, error) {
	hooks := hookset.Map{}
	if raw, ok := top["hooks"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("parse hooks: %w", err)
		}
	}
	return hooks, nil
}

// saveJSONHooks writes the "hooks" section back into hooks.json. Removes the file
// when empty.
func saveJSONHooks(path string, hooks hookset.Map) error {
	top, err := readJSONTop(path)
	if os.IsNotExist(err) {
		top = map[string]json.RawMessage{}
	} else if err != nil {
		return err
	}
	if len(hooks) == 0 {
		delete(top, "hooks")
	} else {
		raw, err := hookset.MarshalNoEscape(hooks)
		if err != nil {
			return err
		}
		top["hooks"] = raw
	}
	if len(top) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	out, err := hookset.MarshalIndentNoEscape(top)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// --- config.toml backend ---

func tomlStore() (store, error) {
	path, err := configTOMLPath()
	if err != nil {
		return store{}, err
	}
	return store{
		path: path,
		load: func() (hookset.Map, error) {
			top, err := readTOMLTop(path)
			if err != nil {
				return nil, err
			}
			defs, _ := splitHooksTable(top["hooks"])
			return hooksFromAny(defs)
		},
		save: func(hooks hookset.Map) error { return saveTOMLHooks(path, hooks) },
	}, nil
}

// readTOMLTop reads config.toml into a generic map. Missing file returns
// os.IsNotExist; empty file yields an empty map.
func readTOMLTop(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	top := map[string]any{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return top, nil
	}
	if err := toml.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return top, nil
}

// saveTOMLHooks writes event definitions back into config.toml's [hooks] table,
// preserving non-definition children and the rest of the config.
func saveTOMLHooks(path string, hooks hookset.Map) error {
	top, err := readTOMLTop(path)
	if os.IsNotExist(err) {
		top = map[string]any{}
	} else if err != nil {
		return err
	}

	_, other := splitHooksTable(top["hooks"])
	merged := map[string]any{}
	for k, v := range other {
		merged[k] = v
	}
	if len(hooks) > 0 {
		defs, err := anyFromHooks(hooks)
		if err != nil {
			return err
		}
		if dm, ok := defs.(map[string]any); ok {
			for k, v := range dm {
				merged[k] = v
			}
		}
	}
	if len(merged) == 0 {
		delete(top, "hooks")
	} else {
		top["hooks"] = merged
	}

	out, err := toml.Marshal(top)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// splitHooksTable separates [hooks] into event definitions (array-valued) and
// other children.
func splitHooksTable(v any) (defs, other map[string]any) {
	defs, other = map[string]any{}, map[string]any{}
	m, ok := v.(map[string]any)
	if !ok {
		return defs, other
	}
	for k, val := range m {
		if _, isArray := val.([]any); isArray {
			defs[k] = val
		} else {
			other[k] = val
		}
	}
	return defs, other
}

// configHasHooks reports whether config.toml defines hook event definitions.
func configHasHooks() (bool, error) {
	path, err := configTOMLPath()
	if err != nil {
		return false, err
	}
	top, err := readTOMLTop(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defs, _ := splitHooksTable(top["hooks"])
	return len(defs) > 0, nil
}

// hooksFromAny converts a decoded generic hooks subtree into the typed hookset
// map via a JSON round trip.
func hooksFromAny(v any) (hookset.Map, error) {
	if v == nil {
		return hookset.Map{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	m := hookset.Map{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// anyFromHooks converts the typed hookset map back to a generic value for TOML
// marshaling.
func anyFromHooks(m hookset.Map) (any, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// loadHooks reads the hooks.json backend. Test seam.
func loadHooks() (hookset.Map, error) {
	s, err := jsonStore()
	if err != nil {
		return nil, err
	}
	return s.load()
}
