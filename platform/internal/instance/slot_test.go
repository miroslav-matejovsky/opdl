package instance_test

import (
	"testing"

	"github.com/miroslav-matejovsky/opdl/platform/internal/instance"
	"github.com/stretchr/testify/require"
)

func TestParseSlot(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in      string
		want    instance.Slot
		wantErr bool
	}{
		"a":         {in: "a", want: instance.SlotA},
		"b":         {in: "b", want: instance.SlotB},
		"empty":     {in: "", wantErr: true},
		"uppercase": {in: "A", wantErr: true},
		"other":     {in: "c", wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := instance.ParseSlot(tc.in)
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

	require.Equal(t, "a", instance.SlotA.String())
	require.Equal(t, "b", instance.SlotB.String())
	require.True(t, instance.SlotA.Valid())
	require.True(t, instance.SlotB.Valid())
	require.False(t, instance.Slot("c").Valid())
	require.False(t, instance.Slot("").Valid())
}

func TestOperationalName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "sensor/a", instance.OperationalName("sensor", instance.SlotA))
	require.Equal(t, "sensor/b", instance.OperationalName("sensor", instance.SlotB))
}

func TestStopOrder(t *testing.T) {
	t.Parallel()

	// A full machine shutdown stops slot B before slot A.
	require.Equal(t, []instance.Slot{instance.SlotB, instance.SlotA}, instance.StopOrder())

	require.True(t, instance.SlotB.StopsBefore(instance.SlotA))
	require.False(t, instance.SlotA.StopsBefore(instance.SlotB))
	require.False(t, instance.SlotA.StopsBefore(instance.SlotA))
	require.False(t, instance.SlotB.StopsBefore(instance.SlotB))
}
