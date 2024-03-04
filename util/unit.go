package util

import (
	"cmp"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/coreos/go-systemd/unit"
)

func CompareOptions(a, b *unit.UnitOption) int {
	c := cmp.Compare(a.Section, b.Section)
	if c != 0 {
		return c
	}
	c = cmp.Compare(a.Name, b.Name)
	if c != 0 {
		return c
	}
	return cmp.Compare(a.Value, b.Value)
}

func ReadUnit(file_path string, sorted bool) ([]*unit.UnitOption, error) {
	// Non-existant file can be "wanted empty" -> just handle the error here
	if _, err := os.Stat(file_path); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	f, err := os.Open(file_path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opts, err := unit.Deserialize(f)
	if err != nil {
		return nil, err
	}
	if sorted {
		slices.SortFunc(opts, CompareOptions)
	}
	return opts, nil
}

func WriteUnit(file_path string, opts []*unit.UnitOption) error {
	exists := true
	if _, err := os.Stat(file_path); os.IsNotExist(err) {
		exists = false
	} else if err != nil {
		return err
	}
	// empty unit files can cause problems
	if len(opts) == 0 {
		if exists {
			return os.Remove(file_path)
		}
		return nil
	}
	// *usually* we are writing overrides or more obscure things and we really need to ensure directory creation
	if err := os.MkdirAll(filepath.Dir(file_path), 0755); err != nil {
		return err
	}
	f, err := os.Create(file_path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, unit.Serialize(opts))
	return err
}

func EnsureUnit(log *slog.Logger, file_path string, in_opts []*unit.UnitOption) (bool, error) {
	unit_opts, err := ReadUnit(file_path, true)
	if err != nil {
		return false, err
	}
	opts := slices.Clone(in_opts)
	slices.SortFunc(opts, CompareOptions)
	add, keep, remove := SliceDiffFunc(opts, unit_opts, CompareOptions)
	if log != nil {
		unit_log := log.With("unit", file_path)
		for _, opt := range add {
			unit_log.Info("Add", LogOption(opt)...)
		}
		for _, opt := range keep {
			unit_log.Debug("Keep", LogOption(opt)...)
		}
		for _, opt := range remove {
			unit_log.Info("Remove", LogOption(opt)...)
		}
	}
	if len(add) == 0 && len(remove) == 0 {
		return false, nil
	}
	return true, WriteUnit(file_path, opts)
}

func LogOption(opt *unit.UnitOption) []any {
	return []any{
		"section",
		opt.Section,
		"option",
		opt.Name,
		"value",
		opt.Value,
	}
}

func SliceDiffFunc[S1 ~[]E1, S2 ~[]E2, E1, E2 any](a S1, b S2, cmp func(E1, E2) int) (add []E1, keep []E2, remove []E2) {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		c := cmp(a[i], b[j])
		if c < 0 {
			add = append(add, a[i])
			i++
		} else if c > 0 {
			remove = append(remove, b[j])
			j++
		} else {
			keep = append(keep, b[j])
			i++
			j++
		}
	}
	for ; i < len(a); i++ {
		add = append(add, a[i])
	}
	for ; j < len(b); j++ {
		remove = append(remove, b[j])
	}
	return

}
