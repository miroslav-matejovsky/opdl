package redundancy_test

import (
	"testing"

	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
	"github.com/stretchr/testify/require"
)

func TestParseSlot(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in      string
		want    redundancy.Slot
		wantErr bool
	}{
		"a":         {in: "a", want: redundancy.SlotA},
		"b":         {in: "b", want: redundancy.SlotB},
		"empty":     {in: "", wantErr: true},
		"uppercase": {in: "A", wantErr: true},
		"other":     {in: "c", wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := redundancy.ParseSlot(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestSlotStringAndValid(t *testing.T) {
	t.Parallel()

	require.Equal(t, "a", redundancy.SlotA.String())
	require.Equal(t, "b", redundancy.SlotB.String())
	require.True(t, redundancy.SlotA.Valid())
	require.True(t, redundancy.SlotB.Valid())
	require.False(t, redundancy.Slot("c").Valid())
	require.False(t, redundancy.Slot("").Valid())
}

func TestOperationalName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "sensor/a", redundancy.OperationalName("sensor", redundancy.SlotA))
	require.Equal(t, "sensor/b", redundancy.OperationalName("sensor", redundancy.SlotB))
}
