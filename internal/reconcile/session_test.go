package reconcile

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDeniedInput(t *testing.T) {
	long := strings.Repeat("é", maxInput+10)
	for _, tc := range []struct{ in, want string }{
		{`{"command":"echo ` + "`x`" + `"}`, "`echo 'x'`"},
		{` {"file_path":"/a"} `, "`{\"file_path\":\"/a\"}`"},
		{`{"command":"` + long + `"}`, "`" + strings.Repeat("é", maxInput) + "…`"},
		{`not json`, "`not json`"},
	} {
		if got := deniedInput(json.RawMessage(tc.in)); got != tc.want {
			t.Errorf("deniedInput(%.40q) = %.60q, want %.60q", tc.in, got, tc.want)
		}
	}
}
