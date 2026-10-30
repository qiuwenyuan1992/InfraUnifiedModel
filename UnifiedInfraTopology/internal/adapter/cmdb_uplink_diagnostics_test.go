package adapter

import (
	"context"
	"reflect"
	"testing"

	"UnifiedInfraTopology/internal/model"
)

func TestCollectServerUplinkDiagnosticsReasonsAndCollectorParity(t *testing.T) {
	port := func(inst int, name, uuid string) map[string]any {
		return map[string]any{"inst_id": inst, "port_name": name, "port_uuid": uuid, "port_type": "physics", "local_device_sn": "tor-a"}
	}
	for _, tc := range []struct {
		name     string
		manifest any
		ports    []map[string]any
		tors     []map[string]any
		want     []string
	}{
		{"resolved_duplicates", uplinkManifest("Ethernet1", "Ethernet1"), []map[string]any{port(10, "Ethernet1", "port-a")}, nil, []string{"", ""}},
		{"missing_port", uplinkManifest("Ethernet2"), []map[string]any{port(10, "Ethernet1", "port-a")}, nil, []string{"cmdb_port_name_missing"}},
		{"blank_port", uplinkManifest(" "), []map[string]any{}, nil, []string{"cmdb_blank_port_name"}},
		{"null_port", []map[string]any{{"sn": "tor-a", "ports": []*string{nil}}}, []map[string]any{}, nil, []string{"cmdb_blank_port_name"}},
		{"blank_tor_entry", []map[string]any{{"sn": "", "ports": []string{"Ethernet1", "Ethernet2"}}}, nil, nil, []string{"cmdb_blank_tor_sn"}},
		{"empty_ports", uplinkManifest(), nil, nil, []string{"cmdb_empty_ports"}},
		{"ambiguous_name", uplinkManifest("Ethernet1"), []map[string]any{port(10, "Ethernet1", "port-a"), port(11, "Ethernet1", "port-b")}, nil, []string{"cmdb_ambiguous_port_name"}},
		{"ambiguous_uuid", uplinkManifest("Ethernet1", "Ethernet2"), []map[string]any{port(10, "Ethernet1", "port-a"), port(11, "Ethernet2", "port-a")}, nil, []string{"cmdb_ambiguous_port_uuid", "cmdb_ambiguous_port_uuid"}},
		{"blank_uuid", uplinkManifest("Ethernet1"), []map[string]any{port(10, "Ethernet1", "")}, nil, []string{"cmdb_blank_port_uuid"}},
		{"ambiguous_tor", uplinkManifest("Ethernet1"), nil, []map[string]any{{"inst_id": 10, "device_sn": "tor-a", "parent_type_id": 54}, {"inst_id": 11, "device_sn": "tor-a", "parent_type_id": 54}}, []string{"cmdb_multiple_network_tors"}},
		{"wrong_tor_identity", uplinkManifest("Ethernet1"), nil, []map[string]any{{"inst_id": 10, "device_sn": "other", "parent_type_id": 54}}, []string{"cmdb_tor_identity_mismatch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmdb := uplinkTestCMDB(t, []map[string]any{uplinkServer(tc.manifest)}, tc.ports, nil, tc.tors...)
			source := model.Source{ID: "s", ConfigRef: "test"}
			result, err := cmdb.CollectServerUplinkDiagnostics(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			if result.Servers != 1 || len(result.SourceDigest) != 64 {
				t.Fatalf("invalid metadata: %+v", result)
			}
			var reasons []string
			resolved := 0
			for _, ref := range result.References {
				reasons = append(reasons, ref.Reason)
				if ref.ServerSN != "server-a" || ref.ServerInstanceID != 1 {
					t.Fatalf("identity lost: %+v", ref)
				}
				if ref.Reason == "" {
					resolved++
					if ref.PortUUID == "" {
						t.Fatal("resolved reference lacks UUID")
					}
				}
			}
			if !reflect.DeepEqual(tc.want, reasons) {
				t.Fatalf("reasons=%v want=%v", reasons, tc.want)
			}
			page, err := cmdb.CollectServerUplinkPage(context.Background(), source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Links) != resolved || page.Skipped != len(result.References)-resolved {
				t.Fatalf("production parity failed: page=%+v diagnostics=%+v", page, result)
			}
			if tc.name == "blank_tor_entry" && len(result.References[0].PortNames) != 2 {
				t.Fatal("blank ToR entry lost raw port names")
			}
		})
	}
}

func TestCollectServerUplinkDiagnosticsCachesReferencedToRAcrossPages(t *testing.T) {
	first, second := uplinkServer(uplinkManifest("Ethernet1")), uplinkServer(uplinkManifest("Ethernet1"))
	second["inst_id"], second["device_sn"] = 2, "server-b"
	portRequests := 0
	cmdb := uplinkTestCMDB(t, []map[string]any{first, second}, []map[string]any{{"inst_id": 10, "port_name": "Ethernet1", "port_uuid": "port-a", "port_type": "physics"}}, func(path string, _ *cmdbEntityRequest) {
		if path != deviceViewEndpoint {
			portRequests++
		}
	})
	result, err := cmdb.CollectServerUplinkDiagnostics(context.Background(), model.Source{ID: "s", ConfigRef: "test"})
	if err != nil || result.Servers != 2 || len(result.References) != 2 || portRequests != 1 {
		t.Fatalf("result=%+v portRequests=%d err=%v", result, portRequests, err)
	}
}

func TestCollectServerUplinkDiagnosticsRejectsCrossPageServerIdentityChanges(t *testing.T) {
	for _, duplicateSN := range []bool{false, true} {
		first, second := uplinkServer(nil), uplinkServer(nil)
		if duplicateSN {
			second["inst_id"] = 2
		} else {
			second["device_sn"] = "server-b"
		}
		cmdb := uplinkTestCMDB(t, []map[string]any{first, second}, nil, nil)
		if _, err := cmdb.CollectServerUplinkDiagnostics(context.Background(), model.Source{ID: "s", ConfigRef: "test"}); err == nil {
			t.Fatal("accepted cross-page duplicate identity/order")
		}
	}
}
