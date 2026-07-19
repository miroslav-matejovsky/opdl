package jsonfields

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Field describes one struct field exposed to JSON encoding.
type Field struct {
	// Name is the encoded JSON property name.
	Name string
	// Type is the Go type of the field.
	Type reflect.Type
	// OmitEmpty is true if the json tag specifies omitempty.
	OmitEmpty bool
}

type candidate struct {
	field Field
	index []int
	depth int
}

// Fields extracts and describes the JSON-exposed fields for the struct type t
// in declaration order, strictly following encoding/json rules.
func Fields(t reflect.Type) ([]Field, error) {
	if t == nil {
		return nil, errors.New("jsonfields: type is nil")
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("jsonfields: expected struct type, got %s", t.Kind())
	}

	candidates, err := collectCandidates(t)
	if err != nil {
		return nil, err
	}

	resolved := resolveConflicts(candidates)
	slices.SortFunc(resolved, func(a, b candidate) int {
		return slices.Compare(a.index, b.index)
	})

	result := make([]Field, len(resolved))
	for i, r := range resolved {
		result[i] = r.field
	}
	return result, nil
}

type queueItem struct {
	typ   reflect.Type
	index []int
	depth int
}

func collectCandidates(t reflect.Type) ([]candidate, error) {
	var candidates []candidate
	queue := []queueItem{{typ: t, index: nil, depth: 0}}
	visited := map[reflect.Type]struct{}{t: {}}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		for i := 0; i < item.typ.NumField(); i++ {
			f := item.typ.Field(i)
			idx := append(slices.Clone(item.index), i)

			if isIgnored(f) {
				continue
			}
			if embed, ok := embeddedStruct(f); ok {
				if _, seen := visited[embed]; !seen {
					visited[embed] = struct{}{}
					queue = append(queue, queueItem{typ: embed, index: idx, depth: item.depth + 1})
				}
				continue
			}
			if !f.IsExported() {
				continue
			}

			c, err := newCandidate(f, idx, item.depth)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, c)
		}
	}
	return candidates, nil
}

func isIgnored(f reflect.StructField) bool {
	tag, hasTag := f.Tag.Lookup("json")
	return hasTag && strings.Split(tag, ",")[0] == "-"
}

func embeddedStruct(f reflect.StructField) (reflect.Type, bool) {
	_, hasTag := f.Tag.Lookup("json")
	if !f.Anonymous || hasTag {
		return nil, false
	}
	ft := f.Type
	if ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	if !f.IsExported() && ft.Kind() != reflect.Struct {
		return nil, false
	}
	if ft.Kind() == reflect.Struct {
		return ft, true
	}
	return nil, false
}

func newCandidate(f reflect.StructField, index []int, depth int) (candidate, error) {
	tag, hasTag := f.Tag.Lookup("json")
	tagParts := strings.Split(tag, ",")
	name := f.Name
	if hasTag && tagParts[0] != "" {
		name = tagParts[0]
	}
	omitempty := false
	if hasTag {
		for _, opt := range tagParts[1:] {
			if opt == "omitempty" {
				omitempty = true
			}
		}
	}
	if err := validateType(f.Type); err != nil {
		return candidate{}, fmt.Errorf("jsonfields: field %q has unsupported type: %w", name, err)
	}
	return candidate{
		field: Field{Name: name, Type: f.Type, OmitEmpty: omitempty},
		index: index,
		depth: depth,
	}, nil
}

func resolveConflicts(candidates []candidate) []candidate {
	byName := make(map[string][]candidate)
	for _, c := range candidates {
		byName[c.field.Name] = append(byName[c.field.Name], c)
	}

	var resolved []candidate
	for _, group := range byName {
		minDepth := group[0].depth
		for _, c := range group[1:] {
			if c.depth < minDepth {
				minDepth = c.depth
			}
		}
		var atMinDepth []candidate
		for _, c := range group {
			if c.depth == minDepth {
				atMinDepth = append(atMinDepth, c)
			}
		}
		if len(atMinDepth) == 1 {
			resolved = append(resolved, atMinDepth[0])
		}
	}
	return resolved
}

func validateType(t reflect.Type) error {
	if t == nil {
		return nil
	}
	switch t.Kind() {
	case reflect.Chan, reflect.Func, reflect.Complex64, reflect.Complex128, reflect.UnsafePointer:
		return fmt.Errorf("unsupported kind %s", t.Kind())
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return validateType(t.Elem())
	case reflect.Map:
		if err := validateType(t.Key()); err != nil {
			return err
		}
		return validateType(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.IsExported() {
				if err := validateType(f.Type); err != nil {
					return fmt.Errorf("field %s: %w", f.Name, err)
				}
			}
		}
	default:
		return nil
	}
	return nil
}
