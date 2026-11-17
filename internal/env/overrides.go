package env

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
)

// ErrNotOverridden is returned by [Overrides.Unset] for a variable that
// was never set through the same [Overrides].
var ErrNotOverridden = errors.New("variable was not set at runtime")

// ValidateName reports whether name is usable as an environment variable
// name that a shell can expand as $NAME: a letter or underscore followed
// by letters, digits or underscores.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("name is empty")
	}
	for i, r := range name {
		isLetter := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		if !isLetter && (i == 0 || !isDigit) {
			return fmt.Errorf("name %q must start with a letter or underscore and contain only letters, digits and underscores", name)
		}
	}
	return nil
}

// Overrides sets environment variables on the running process, which the
// commands it spawns inherit, and remembers what each one replaced so it
// can be put back. Nothing is persisted: the changes last as long as the
// process does. The zero value is ready to use.
type Overrides struct {
	mu    sync.Mutex
	saved map[string]previous
}

// previous is what a variable held before the first override, so Unset
// can restore it.
type previous struct {
	value string
	set   bool
}

// Set sets name to value in the process environment. Overriding a name
// again keeps the value it had before the first override.
func (o *Overrides) Set(name, value string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if strings.ContainsRune(value, 0) {
		return errors.New("value contains a NUL byte")
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	prev, had := os.LookupEnv(name)
	if err := os.Setenv(name, value); err != nil {
		return err
	}
	if _, ok := o.saved[name]; !ok {
		if o.saved == nil {
			o.saved = make(map[string]previous)
		}
		o.saved[name] = previous{value: prev, set: had}
	}
	return nil
}

// Unset restores name to what it held before it was first overridden, or
// removes it if it did not exist. A name that was never overridden is left
// alone and reported as [ErrNotOverridden].
func (o *Overrides) Unset(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	prev, ok := o.saved[name]
	if !ok {
		return fmt.Errorf("%s: %w", name, ErrNotOverridden)
	}
	var err error
	if prev.set {
		err = os.Setenv(name, prev.value)
	} else {
		err = os.Unsetenv(name)
	}
	if err != nil {
		return err
	}
	delete(o.saved, name)
	return nil
}

// Names returns the overridden variable names in alphabetical order.
func (o *Overrides) Names() []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	names := make([]string, 0, len(o.saved))
	for name := range o.saved {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
