package service

import (
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestInventoryAuthRequiresExplicitBooleanFalse(t *testing.T) {
	for _, value := range []any{nil, true, "false", 0, false} {
		conf := viper.New()
		if value != nil {
			conf.Set("inventory.auth.enabled", value)
		}
		disabled := value == false
		require.Equal(t, disabled, InventoryAuthDisabled(conf))
		s := NewInventoryService(nil, conf).(*inventoryService)
		for _, resource := range []string{"sources", "sync-runs", "write"} {
			if disabled {
				require.NoError(t, s.authorize(InventoryAnonymousUser, resource))
				require.Error(t, s.authorize("arbitrary-user", resource))
			} else {
				require.Error(t, s.authorize(InventoryAnonymousUser, resource))
			}
		}
	}
	require.False(t, InventoryAuthDisabled(nil))
}
