package operations

import (
	"encoding/json"
	"testing"
)

func TestResolveDataMountRejectsUnsafeStorage(t *testing.T) {
	cases := []struct{ name, mounts string }{
		{"missing", `[]`},
		{"read-only bind", `[{"Type":"bind","Source":"/srv/services/data","Destination":"/data","RW":false}]`},
		{"read-only volume", `[{"Type":"volume","Name":"services-data","Destination":"/data","RW":false}]`},
		{"unnamed volume", `[{"Type":"volume","Destination":"/data","RW":true}]`},
		{"empty bind source", `[{"Type":"bind","Destination":"/data","RW":true}]`},
		{"relative bind source", `[{"Type":"bind","Source":"./data","Destination":"/data","RW":true}]`},
		{"drive-relative source", `[{"Type":"bind","Source":"E:data","Destination":"/data","RW":true}]`},
		{"host root", `[{"Type":"bind","Source":"/","Destination":"/data","RW":true}]`},
		{"temporary storage", `[{"Type":"tmpfs","Source":"tmpfs","Destination":"/data","RW":true}]`},
		{"wrong destination", `[{"Type":"bind","Source":"/srv/services/data","Destination":"/other","RW":true}]`},
		{"duplicate data mounts", `[{"Type":"volume","Name":"services-data","Destination":"/data","RW":true},{"Type":"bind","Source":"/srv/services/data","Destination":"/data","RW":true}]`},
		{"shadowed database", `[{"Type":"volume","Name":"services-data","Destination":"/data","RW":true},{"Type":"bind","Source":"/tmp/different.db","Destination":"/data/operations.db","RW":true}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var inspected Inspect
			if err := json.Unmarshal([]byte(`{"Mounts":`+tc.mounts+`}`), &inspected); err != nil {
				t.Fatal(err)
			}
			if got, err := ResolveDataMount(inspected); err == nil {
				t.Fatalf("unsafe storage accepted: %+v", got)
			}
		})
	}
}
