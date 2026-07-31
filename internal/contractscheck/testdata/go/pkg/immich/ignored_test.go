package immich

import "encoding/json"

func testOnlyMapMarshal() error {
	_, err := json.Marshal(map[string]string{"test": "only"})
	return err
}
