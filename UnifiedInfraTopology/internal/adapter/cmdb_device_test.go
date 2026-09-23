package adapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
)

func TestDeviceViewDTOToRecordUsesDeviceSNAndStableUniqueIPs(t *testing.T) {
	dto := deviceViewDTO{
		InstID:       101,
		DeviceID:     101,
		DeviceSN:     " SN-001 ",
		DeviceName:   " primary-name ",
		HostName:     "fallback-host",
		ParentTypeID: 52,
		DeviceTypeID: 57,
		Role:         "worker",
		CabinetUUID:  "cabinet-uuid",
		PodUUID:      "pod-uuid",
		ComputePlane: []deviceComputePlaneDTO{{PodID: 9}},
		DeviceIPInfo: deviceIPInfoDTO{
			Eth:        []deviceIPDTO{{IP: "10.0.0.1"}},
			ILO:        []deviceIPDTO{{IP: "2001:0db8::1"}},
			Data:       []deviceIPDTO{{IP: "10.0.0.1"}},
			Console:    []deviceIPDTO{{IP: ""}},
			Management: []deviceIPDTO{{IP: "10.0.0.2"}},
		},
		EthIP:        []string{"10.0.0.2", "10.0.0.3", "10.0.0.7:8017", "10.0.0.8:8003/8006", `10.0.0.9:8003\8006`, "11.4.2.41:800911.4.2.41"},
		ILOIP:        []string{"2001:db8::1"},
		DataIP:       []string{"10.0.0.4"},
		ConsoleIP:    []string{"10.0.0.5"},
		ManagementIP: []string{"10.0.0.6"},
	}

	record, err := dto.toRecord()

	require.NoError(t, err)
	require.Equal(t, int64(101), record.SourceInstanceID)
	require.Equal(t, "SN-001", record.DeviceSN)
	require.Equal(t, "primary-name", record.Name)
	require.Equal(t, int64(52), record.ParentTypeID)
	require.Equal(t, int64(57), record.DeviceTypeID)
	require.Equal(t, "worker", record.Role)
	require.Equal(t, "cabinet-uuid", record.CabinetUUID)
	require.Equal(t, "pod-uuid", record.PodUUID)
	require.Equal(t, []ComputePlaneReference{{PodID: 9}}, record.ComputePlane)
	require.Equal(t, []string{"10.0.0.1", "2001:db8::1", "10.0.0.2", "10.0.0.3", "10.0.0.7", "10.0.0.8", "10.0.0.9", "10.0.0.4", "10.0.0.5", "10.0.0.6"}, record.AllIPs)
}

func TestDeviceViewDTOToRecordFallsBackToHostNameThenSN(t *testing.T) {
	tests := []struct {
		name       string
		deviceName string
		hostName   string
		want       string
	}{
		{name: "host name", hostName: "host-1", want: "host-1"},
		{name: "device sn", want: "SN-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, err := (deviceViewDTO{
				InstID: 1, DeviceID: 1, DeviceSN: "SN-1",
				DeviceName: tt.deviceName, HostName: tt.hostName,
				ParentTypeID: 52, DeviceTypeID: 57,
			}).toRecord()
			require.NoError(t, err)
			require.Equal(t, tt.want, record.Name)
		})
	}
}

func TestDeviceViewDTOToRecordAllowsMissingOptionalTypeFields(t *testing.T) {
	record, err := (deviceViewDTO{InstID: 1, DeviceID: 1, DeviceSN: "SN-1"}).toRecord()
	require.NoError(t, err)
	require.Zero(t, record.ParentTypeID)
	require.Zero(t, record.DeviceTypeID)
}

func TestDeviceViewDTOToRecordRejectsInvalidRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		dto  deviceViewDTO
	}{
		{name: "missing sn", dto: deviceViewDTO{InstID: 1, DeviceID: 1, ParentTypeID: 52, DeviceTypeID: 57}},
		{name: "missing reference", dto: deviceViewDTO{DeviceSN: "SN-1", ParentTypeID: 52, DeviceTypeID: 57}},
		{name: "missing inst id", dto: deviceViewDTO{DeviceID: 1, DeviceSN: "SN-1", ParentTypeID: 52, DeviceTypeID: 57}},
		{name: "missing device id", dto: deviceViewDTO{InstID: 1, DeviceSN: "SN-1", ParentTypeID: 52, DeviceTypeID: 57}},
		{name: "conflicting reference", dto: deviceViewDTO{InstID: 1, DeviceID: 2, DeviceSN: "SN-1", ParentTypeID: 52, DeviceTypeID: 57}},
		{name: "invalid parent type", dto: deviceViewDTO{InstID: 1, DeviceID: 1, DeviceSN: "SN-1", ParentTypeID: 99, DeviceTypeID: 57}},
		{name: "negative device type", dto: deviceViewDTO{InstID: 1, DeviceID: 1, DeviceSN: "SN-1", ParentTypeID: 52, DeviceTypeID: -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.dto.toRecord()
			require.ErrorIs(t, err, ErrCMDBRecord)
		})
	}
}

func TestCMDBCollectDevicesFailsRoundOnInvalidDevice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"result":true,"error_code":0,"data":{"count":1,"info":[{"inst_id":1,"device_id":2,"device_sn":"SN-1","parent_type_id":52,"device_type_id":57}]}}`)
	}))
	defer server.Close()

	visited := false
	err := NewCMDBWithClient(cmdbTestConfig(server.URL, nil), server.Client()).CollectDevices(
		context.Background(),
		model.Source{ConfigRef: "primary"},
		func(DeviceRecord) error {
			visited = true
			return nil
		},
	)

	require.ErrorIs(t, err, ErrCMDBRecord)
	require.False(t, visited)
}
