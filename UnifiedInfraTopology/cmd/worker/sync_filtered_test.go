package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestSyncFilteredRejectsUnsafeRequestsBeforeConnections(t *testing.T) {
	for _, tc := range []struct {
		source string
		entity model.EntityType
		want   string
	}{
		{source: "invalid", entity: model.EntityCabinet, want: "filtered sync requires a valid source ID and device, data_center, cabinet, interface, or transformer"},
		{source: strings.Repeat("a", 32), entity: model.EntityDevice, want: "filtered sync requires the configured MySQL control database"},
		{source: strings.Repeat("a", 32), entity: model.EntityCabinet, want: "filtered sync requires the configured MySQL control database"},
	} {
		_, err := syncFiltered(context.Background(), viper.New(), nil, tc.source, tc.entity, io.Discard)
		require.EqualError(t, err, tc.want)
	}
}
