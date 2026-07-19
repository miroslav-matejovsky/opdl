package jsonfields_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/miroslav-matejovsky/opdl/utils/jsonfields"
	"github.com/stretchr/testify/require"
)

type InnerA struct {
	Promoted string `json:"promoted"`
}

type SampleStruct struct {
	Tagged     string `json:"tagged_name"`
	Untagged   int
	Skipped    bool   `json:"-"`
	OmitEmpty  string `json:"omit,omitempty"`
	WithOption int    `json:"with_string,string"`
	//nolint:unused // unexported field verified by reflection to be ignored by json encoding
	unexported string
	EmptyTag   string `json:""`
	Pointer    *int   `json:"ptr"`
	InnerA
}

func TestFields_BasicAndPromotedRules(t *testing.T) {
	t.Parallel()

	fields, err := jsonfields.Fields(reflect.TypeOf(SampleStruct{}))
	require.NoError(t, err)

	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}

	// Declaration order: Tagged, Untagged, OmitEmpty, WithOption, EmptyTag, Pointer, then Promoted from InnerA.
	// Skipped and unexported fields should not be present.
	expectedNames := []string{"tagged_name", "Untagged", "omit", "with_string", "EmptyTag", "ptr", "promoted"}
	require.Equal(t, expectedNames, names)

	// Check omitempty flag.
	for _, f := range fields {
		if f.Name == "omit" {
			require.True(t, f.OmitEmpty)
		} else {
			require.False(t, f.OmitEmpty)
		}
	}
}

func TestFields_ConflictingPromotedFields(t *testing.T) {
	t.Parallel()

	type conflictA struct {
		ID string `json:"conflict"`
	}
	type conflictB struct {
		ID string `json:"conflict"`
	}
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeOf(conflictA{}), Anonymous: true},
		{Name: "B", Type: reflect.TypeOf(conflictB{}), Anonymous: true},
	})

	fields, err := jsonfields.Fields(typ)
	require.NoError(t, err)
	require.Empty(t, fields, "conflicting fields at the same depth must cancel out and be ignored")
}

func TestFields_EncodingJSONEmbeddingRules(t *testing.T) {
	t.Parallel()

	type common struct {
		Value string `json:"value"`
	}
	type left struct{ common }
	type right struct{ common }
	type diamond struct {
		left
		right
	}

	fields, err := jsonfields.Fields(reflect.TypeFor[diamond]())
	require.NoError(t, err)
	require.Empty(t, fields, "the same field promoted through two paths is ambiguous")

	type untagged struct{ Value string }
	type tagged struct {
		Value string `json:"Value"`
	}
	type dominant struct {
		untagged
		tagged
	}

	fields, err = jsonfields.Fields(reflect.TypeFor[dominant]())
	require.NoError(t, err)
	require.Equal(t, []jsonfields.Field{{Name: "Value", Type: reflect.TypeFor[string]()}}, fields)

	type optionOnly struct {
		InnerA `json:",omitempty"`
	}
	fields, err = jsonfields.Fields(reflect.TypeFor[optionOnly]())
	require.NoError(t, err)
	require.Equal(t, []jsonfields.Field{{Name: "promoted", Type: reflect.TypeFor[string]()}}, fields)
}

func TestFields_RecursiveEmbeddedStructTerminates(t *testing.T) {
	t.Parallel()

	type recursive struct{ *recursive }
	fields, err := jsonfields.Fields(reflect.TypeFor[recursive]())
	require.NoError(t, err)
	require.Empty(t, fields)
}

func TestFields_CompareWithEncodingJSON(t *testing.T) {
	t.Parallel()

	val := SampleStruct{
		Tagged:     "t",
		Untagged:   1,
		OmitEmpty:  "o",
		WithOption: 42,
		EmptyTag:   "e",
		Pointer:    new(int),
		InnerA:     InnerA{Promoted: "p"},
	}

	data, err := json.Marshal(val)
	require.NoError(t, err)

	var encoded map[string]any
	require.NoError(t, json.Unmarshal(data, &encoded))

	fields, err := jsonfields.Fields(reflect.TypeOf(SampleStruct{}))
	require.NoError(t, err)

	discoveredSet := make(map[string]struct{})
	for _, f := range fields {
		discoveredSet[f.Name] = struct{}{}
	}

	encodedSet := make(map[string]struct{})
	for k := range encoded {
		encodedSet[k] = struct{}{}
	}

	require.Equal(t, encodedSet, discoveredSet, "discovered field names must match encoding/json output keys exactly")
}

func TestFields_UnsupportedTypesAndErrors(t *testing.T) {
	t.Parallel()

	_, err := jsonfields.Fields(nil)
	require.Error(t, err)

	_, err = jsonfields.Fields(reflect.TypeOf(123))
	require.Error(t, err)

	type Unsupported struct {
		Bad chan int `json:"bad"`
	}
	_, err = jsonfields.Fields(reflect.TypeOf(Unsupported{}))
	require.Error(t, err)
}
