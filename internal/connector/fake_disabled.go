//go:build !e2e

package connector

import "fmt"

func newFakeConnectorFromConfig(_ map[string]string) (Connector, error) {
	return nil, fmt.Errorf("fake connector requires an e2e build (`-tags=e2e`)")
}
