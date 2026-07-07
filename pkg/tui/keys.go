package tui

// defaultKeybinds maps each rebindable composer action to its default key. Core
// keys (enter to send, ctrl+c to quit, esc to interrupt, @ for the file picker)
// are fixed and not listed here, so a bad config cannot lock the user out.
var defaultKeybinds = map[string]string{
	"model_cycle":     "ctrl+n",
	"reasoning_cycle": "shift+tab",
	"paste_image":     "ctrl+v",
	"editor":          "ctrl+g",
}

// keymap resolves a pressed key to the composer action bound to it.
type keymap struct {
	byKey map[string]string // key -> action
}

// newKeymap starts from the defaults and applies overrides. An override for an
// unknown action, or an empty key, is ignored so the default survives.
func newKeymap(override map[string]string) keymap {
	binds := make(map[string]string, len(defaultKeybinds))
	for action, key := range defaultKeybinds {
		binds[action] = key
	}
	for action, key := range override {
		if _, ok := defaultKeybinds[action]; ok && key != "" {
			binds[action] = key
		}
	}
	byKey := make(map[string]string, len(binds))
	for action, key := range binds {
		byKey[key] = action
	}
	return keymap{byKey: byKey}
}

// action returns the composer action bound to a pressed key, or "" when the key
// is not bound.
func (k keymap) action(key string) string { return k.byKey[key] }
