package logscan

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFields(t *testing.T) {
	tests := []struct {
		name   string
		logs   string
		prefix string
		want   []map[string]string
	}{
		{
			name:   "absent prefix",
			logs:   "some log line without prefix\nanother line\n",
			prefix: "CFG: ",
			want:   nil,
		},
		{
			name:   "multiple occurrences",
			logs:   "info CFG: k1=v1 k2=v2\nother\ninfo CFG: k1=v3 k3=v4\r\n",
			prefix: "CFG: ",
			want: []map[string]string{
				{"k1": "v1", "k2": "v2"},
				{"k1": "v3", "k3": "v4"},
			},
		},
		{
			name:   "unparsable field and empty value",
			logs:   "CFG: good=yes badtoken empty=\n",
			prefix: "CFG: ",
			want: []map[string]string{
				{"good": "yes", "empty": ""},
			},
		},
		{
			name:   "duplicate field on one line",
			logs:   "CFG: k=first k=second\n",
			prefix: "CFG: ",
			want: []map[string]string{
				{"k": "second"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Fields(tc.logs, tc.prefix)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestAfter(t *testing.T) {
	tests := []struct {
		name   string
		logs   string
		marker string
		want   []string
	}{
		{
			name:   "absent marker",
			logs:   "hello world\n",
			marker: "journal ",
			want:   nil,
		},
		{
			name:   "marker with token and trailing carriage return",
			logs:   "starting journal my-site-journal \r\nnext line journal other-journal\n",
			marker: "journal ",
			want:   []string{"my-site-journal", "other-journal"},
		},
		{
			name:   "marker at end of line",
			logs:   "line ends with journal \r\n",
			marker: "journal ",
			want:   []string{""},
		},
		{
			name:   "multiple spaces before token",
			logs:   "info journal    token-after-spaces\n",
			marker: "journal ",
			want:   []string{"token-after-spaces"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := After(tc.logs, tc.marker)
			require.Equal(t, tc.want, got)
		})
	}
}
