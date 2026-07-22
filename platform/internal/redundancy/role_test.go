package redundancy_test

import (
	"testing"

	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
	"github.com/stretchr/testify/require"
)

func TestParseRole(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in      string
		want    redundancy.InstanceRole
		wantErr bool
	}{
		"primary":   {in: "primary", want: redundancy.RolePrimary},
		"standby":   {in: "standby", want: redundancy.RoleStandby},
		"empty":     {in: "", wantErr: true},
		"uppercase": {in: "PRIMARY", wantErr: true},
		"other":     {in: "other", wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := redundancy.ParseRole(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestRoleStringAndValid(t *testing.T) {
	t.Parallel()

	require.Equal(t, "primary", redundancy.RolePrimary.String())
	require.Equal(t, "standby", redundancy.RoleStandby.String())
	require.True(t, redundancy.RolePrimary.Valid())
	require.True(t, redundancy.RoleStandby.Valid())
	require.False(t, redundancy.InstanceRole("other").Valid())
	require.False(t, redundancy.InstanceRole("").Valid())
}

func TestOperationalName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "sensor/primary", redundancy.OperationalName("sensor", redundancy.RolePrimary))
	require.Equal(t, "sensor/standby", redundancy.OperationalName("sensor", redundancy.RoleStandby))
}
