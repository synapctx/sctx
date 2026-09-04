package hook

import (
	"reflect"
	"testing"
)

func TestChangedExportedSymbols(t *testing.T) {
	tests := []struct {
		name      string
		filePath  string
		oldString string
		newString string
		want      []string
	}{
		{
			name:      "new exported func",
			filePath:  "svc/handler.go",
			oldString: "",
			newString: "func DoThing(x int) error {\n\treturn nil\n}\n",
			want:      []string{"DoThing"},
		},
		{
			name:      "changed exported func signature",
			filePath:  "svc/handler.go",
			oldString: "func DoThing(x int) error {\n",
			newString: "func DoThing(x int, y string) error {\n",
			want:      []string{"DoThing"},
		},
		{
			name:      "unexported func ignored",
			filePath:  "svc/handler.go",
			oldString: "func doThing(x int) error {\n",
			newString: "func doThing(x int, y string) error {\n",
			want:      nil,
		},
		{
			name:      "exported method changed",
			filePath:  "svc/handler.go",
			oldString: "func (h *Handler) Serve(w http.ResponseWriter) {\n",
			newString: "func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {\n",
			want:      []string{"Serve"},
		},
		{
			name:      "unexported method ignored",
			filePath:  "svc/handler.go",
			oldString: "func (h *Handler) serve(w http.ResponseWriter) {\n",
			newString: "func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {\n",
			want:      nil,
		},
		{
			name:      "exported type declaration line changed",
			filePath:  "svc/model.go",
			oldString: "type Widget struct {\n\tName string\n}\n",
			newString: "type Widget[T any] struct {\n\tName string\n}\n",
			want:      []string{"Widget"},
		},
		{
			name:      "unchanged declaration line not reported",
			filePath:  "svc/handler.go",
			oldString: "func DoThing(x int) error {\n\treturn nil\n}\n",
			newString: "func DoThing(x int) error {\n\treturn fmt.Errorf(\"nope\")\n}\n",
			want:      nil,
		},
		{
			name:      "non-go file ignored",
			filePath:  "svc/handler.py",
			oldString: "def do_thing(x):\n",
			newString: "def do_thing(x, y):\n",
			want:      nil,
		},
		{
			name:      "cap at three",
			filePath:  "svc/handler.go",
			oldString: "",
			newString: "func Alpha() {}\nfunc Bravo() {}\nfunc Charlie() {}\nfunc Delta() {}\n",
			want:      []string{"Alpha", "Bravo", "Charlie"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := changedExportedSymbols(tt.filePath, tt.oldString, tt.newString)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("changedExportedSymbols() = %v, want %v", got, tt.want)
			}
		})
	}
}
