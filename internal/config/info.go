package config

import (
	"errors"
	"fmt"
	"slices"
)

// ErrInvalidInfoItem is returned when an info item is malformed.
var ErrInvalidInfoItem = errors.New("invalid info item")

// mergeInfo applies src items on top of dst: an item replaces the one with
// the same name in place, an item without a cmd removes it, and new names are
// appended. Nameless items are kept so validateInfo can report them.
func mergeInfo(dst, src []InfoItem) []InfoItem {
	for _, item := range src {
		i := slices.IndexFunc(dst, func(d InfoItem) bool { return item.Name != "" && d.Name == item.Name })
		switch {
		case i < 0 && item.Cmd.IsEmpty():
			// Removing an unknown item is a no-op.
		case i < 0:
			dst = append(dst, item)
		case item.Cmd.IsEmpty():
			dst = slices.Delete(dst, i, i+1)
		default:
			dst[i] = item
		}
	}
	return dst
}

// validateInfo checks that every info item has a name.
func validateInfo(items []InfoItem) error {
	for i, item := range items {
		if item.Name == "" {
			return fmt.Errorf("%w: item #%d (%s) has no name", ErrInvalidInfoItem, i+1, item.Cmd.String())
		}
	}
	return nil
}
