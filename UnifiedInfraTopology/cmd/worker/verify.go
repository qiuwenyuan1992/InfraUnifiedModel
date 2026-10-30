package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	_ "github.com/go-sql-driver/mysql"
	"github.com/spf13/viper"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	nebulatype "github.com/vesoft-inc/nebula-go/v3/nebula"
)

type persistenceCursor struct {
	Version    int       `json:"version"`
	RunID      string    `json:"run_id"`
	SnapshotAt time.Time `json:"snapshot_at"`
	NextOffset int64     `json:"next_offset"`
	Total      int64     `json:"total"`
}
type persistenceRun struct {
	ID             string     `json:"id"`
	SourceID       string     `json:"source_id"`
	Status         string     `json:"status"`
	StartedAt      *time.Time `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at"`
}
type persistenceCheckpoint struct {
	Complete    bool              `json:"complete"`
	CompletedAt *time.Time        `json:"completed_at"`
	Cursor      persistenceCursor `json:"cursor"`
}
type persistenceSample struct {
	VID                 string    `json:"vid"`
	StableID            string    `json:"stable_id"`
	Code                string    `json:"code,omitempty"`
	PortSpeed           *string   `json:"port_speed,omitempty"`
	PortOperationStatus *string   `json:"port_operation_status,omitempty"`
	PortType            *string   `json:"port_type,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	SyncedAt            time.Time `json:"synced_at"`
}
type persistenceReport struct {
	Status         string                 `json:"status"`
	SourceID       string                 `json:"source_id"`
	Entity         model.EntityType       `json:"entity_type"`
	ConfigRef      string                 `json:"config_ref"`
	Resource       string                 `json:"resource"`
	ActiveRuns     int64                  `json:"active_runs_global"`
	Run            *persistenceRun        `json:"run,omitempty"`
	Checkpoint     *persistenceCheckpoint `json:"checkpoint,omitempty"`
	GraphTotal     int64                  `json:"graph_total"`
	GraphCurrent   int64                  `json:"graph_current_snapshot"`
	ObjectsPresent bool                   `json:"objects_present"`
	Samples        []persistenceSample    `json:"samples"`
	Note           string                 `json:"note,omitempty"`
	Ownership      *ownershipReport       `json:"ownership,omitempty"`
}

func persistenceGraphQuery(entity model.EntityType, current, count bool) (string, error) {
	identity := "uuid"
	switch entity {
	case model.EntityDevice:
		identity = "device_sn"
	case model.EntityInterface:
		identity = "port_uuid"
	case model.EntityDataCenter, model.EntityCabinet, model.EntityTransformer:
	default:
		return "", errors.New("unsupported verification entity")
	}
	tag := string(entity)
	query := "LOOKUP ON " + tag + " WHERE " + tag + ".source_id == $source_id"
	if current {
		query += " AND " + tag + ".synced_at == datetime($snapshot)"
	}
	query += " YIELD id(vertex) AS vid"
	if count {
		return query + " | YIELD count(*) AS total;", nil
	}
	query += ", " + tag + "." + identity + " AS identity, toString(" + tag + ".created_at) AS created_at, toString(" + tag + ".synced_at) AS synced_at"
	if entity == model.EntityInterface {
		query += ", interface.port_name AS code, interface.port_speed AS port_speed, interface.port_operation_status AS port_operation_status, interface.port_type AS port_type"
	} else if entity != model.EntityDevice {
		query += ", " + tag + ".code AS code"
	}
	return query + ";", nil
}

func persistenceCheckGraphResult(result *nebulago.ResultSet, err error) error {
	if err != nil || result == nil || !result.IsSucceed() {
		return errors.New("graph verification query failed")
	}
	return nil
}

func validatePersistenceReport(r *persistenceReport, rows []persistenceSample) error {
	fail := func() error { return errors.New("persisted run, checkpoint, or graph snapshot does not match") }
	if r.Run == nil || r.Checkpoint == nil {
		return fail()
	}
	run, checkpoint := r.Run, r.Checkpoint
	cursor := checkpoint.Cursor
	if run.SourceID != r.SourceID || run.Status != model.SyncRunStatusSucceeded || run.StartedAt == nil || run.FinishedAt == nil || run.LeaseExpiresAt != nil || run.FinishedAt.Before(*run.StartedAt) || !checkpoint.Complete || checkpoint.CompletedAt == nil || cursor.Version != 1 || cursor.RunID != run.ID || !cursor.SnapshotAt.Equal(*run.StartedAt) || cursor.Total < 0 || cursor.NextOffset != cursor.Total || r.GraphCurrent != cursor.Total || r.GraphTotal < r.GraphCurrent || int64(len(rows)) != r.GraphCurrent {
		return fail()
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.StableID) == "" || strings.TrimSpace(row.StableID) != row.StableID || row.CreatedAt.IsZero() || row.CreatedAt.After(row.SyncedAt) || !row.SyncedAt.Equal(cursor.SnapshotAt) || seen[row.VID] {
			return fail()
		}
		vid, err := (model.EntityIdentity{SourceID: r.SourceID, EntityType: r.Entity, StableID: row.StableID}).VID()
		if err != nil || vid != row.VID {
			return fail()
		}
		seen[row.VID] = true
	}
	r.Status = "PASS"
	r.ObjectsPresent = r.GraphCurrent > 0
	if !r.ObjectsPresent {
		r.Note = "verified empty snapshot; no objects were stored"
	}
	r.Samples = rows
	if len(r.Samples) > 5 {
		r.Samples = r.Samples[:5]
	}
	return nil
}

func verifyPersistence(ctx context.Context, conf *viper.Viper, sourceID string, entity model.EntityType, runID string, output io.Writer) error {
	if conf.GetString("data.db.main.driver") != "mysql" {
		return errors.New("verification requires mysql")
	}
	if !inventoryID.MatchString(sourceID) || (runID != "" && !inventoryID.MatchString(runID)) {
		return errors.New("verification requires valid source and run IDs")
	}
	query, err := persistenceGraphQuery(entity, false, true)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", conf.GetString("data.db.main.dsn"))
	if err != nil {
		return errors.New("control database configuration invalid")
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return errors.New("control database connection failed")
	}
	r := persistenceReport{Status: "preflight", SourceID: sourceID, Entity: entity, Samples: []persistenceSample{}}
	switch entity {
	case model.EntityDevice:
		r.Resource = "devices"
	case model.EntityDataCenter:
		r.Resource = "data_centers"
	case model.EntityCabinet:
		r.Resource = "cabinets"
	case model.EntityInterface:
		r.Resource = "interfaces"
	case model.EntityTransformer:
		r.Resource = "transformers"
	}
	var enabled bool
	if err := db.QueryRowContext(ctx, "SELECT config_ref, enabled FROM sources WHERE id = ?", sourceID).Scan(&r.ConfigRef, &enabled); err != nil {
		return errors.New("verification source not found")
	}
	if !enabled {
		return errors.New("verification source is disabled")
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sync_runs WHERE status IN ('queued','running','validating','publishing')").Scan(&r.ActiveRuns); err != nil {
		return errors.New("active run query failed")
	}
	params := map[string]interface{}{"source_id": sourceID}
	if runID != "" {
		r.Run = &persistenceRun{}
		if err := db.QueryRowContext(ctx, "SELECT id, source_id, status, started_at, finished_at, lease_expires_at FROM sync_runs WHERE id = ?", runID).Scan(&r.Run.ID, &r.Run.SourceID, &r.Run.Status, &r.Run.StartedAt, &r.Run.FinishedAt, &r.Run.LeaseExpiresAt); err != nil {
			return errors.New("verification run not found")
		}
		r.Checkpoint = &persistenceCheckpoint{}
		var cursor string
		if err := db.QueryRowContext(ctx, "SELECT complete, completed_at, `cursor` FROM sync_checkpoints WHERE source_id = ? AND resource = ?", sourceID, r.Resource).Scan(&r.Checkpoint.Complete, &r.Checkpoint.CompletedAt, &cursor); err != nil {
			return errors.New("verification checkpoint not found")
		}
		if err := json.Unmarshal([]byte(cursor), &r.Checkpoint.Cursor); err != nil {
			return errors.New("verification cursor invalid")
		}
		params["snapshot"] = r.Checkpoint.Cursor.SnapshotAt.UTC().Format("2006-01-02T15:04:05.000000+00:00")
	}
	graphConfig, err := graphclient.ParseConfig(conf)
	if err != nil {
		return errors.New("graph configuration invalid")
	}
	client, err := graphclient.New(graphConfig)
	if err != nil {
		return errors.New("graph connection failed")
	}
	defer client.Close()
	read := func(statement string) ([][]string, error) {
		result, err := client.ExecuteParameter(ctx, statement, params)
		if err := persistenceCheckGraphResult(result, err); err != nil {
			return nil, err
		}
		return persistenceTable(result)
	}
	count := func(statement string) (int64, error) {
		rows, err := read(statement)
		if err != nil {
			return 0, err
		}
		if len(rows) != 2 || len(rows[1]) != 1 {
			return 0, errors.New("invalid graph count response")
		}
		return strconv.ParseInt(rows[1][0], 10, 64)
	}
	r.GraphTotal, err = count(query)
	if err != nil {
		return err
	}
	if runID == "" {
		return json.NewEncoder(output).Encode(r)
	}
	query, _ = persistenceGraphQuery(entity, true, true)
	r.GraphCurrent, err = count(query)
	if err != nil {
		return err
	}
	query, _ = persistenceGraphQuery(entity, true, false)
	rows, err := read(query)
	if err != nil {
		return err
	}
	var samples []persistenceSample
	for _, row := range rows[1:] {
		if len(row) < 4 {
			return errors.New("invalid graph vertex response")
		}
		sample := persistenceSample{VID: persistenceString(row[0]), StableID: persistenceString(row[1])}
		sample.CreatedAt, err = persistenceTime(row[2])
		if err != nil {
			return err
		}
		sample.SyncedAt, err = persistenceTime(row[3])
		if err != nil {
			return err
		}
		if len(row) > 4 {
			sample.Code = persistenceString(row[4])
		}
		if entity == model.EntityInterface {
			if len(row) != 8 {
				return errors.New("invalid interface property response")
			}
			for index, target := range []**string{&sample.PortSpeed, &sample.PortOperationStatus, &sample.PortType} {
				*target, err = persistenceNullableText(row[5+index])
				if err != nil {
					return err
				}
			}
			expectedType := conf.GetString("inventory.cmdb.sources." + r.ConfigRef + ".interface_condition.port_type")
			if expectedType != "" && (sample.PortType == nil || *sample.PortType != expectedType) {
				return errors.New("stored interface does not match selected port type")
			}
		}
		samples = append(samples, sample)
	}
	if err := validatePersistenceReport(&r, samples); err != nil {
		r.Status = "FAIL"
		_ = json.NewEncoder(output).Encode(r)
		return err
	}
	if entity == model.EntityInterface {
		r.Ownership, err = verifyOwnership(ctx, client, sourceID, r.Checkpoint.Cursor.SnapshotAt, r.GraphCurrent)
		if err != nil {
			r.Status = "FAIL"
			_ = json.NewEncoder(output).Encode(r)
			return err
		}
	}
	return json.NewEncoder(output).Encode(r)
}

// 通过 SDK 类型读取后编码，不能把未转义的 AsStringTable 文本当作 JSON。
func persistenceTable(result *nebulago.ResultSet) ([][]string, error) {
	table := make([][]string, result.GetRowSize()+1)
	table[0] = make([]string, result.GetColSize())
	for rowIndex := 0; rowIndex < result.GetRowSize(); rowIndex++ {
		record, err := result.GetRowValuesByIndex(rowIndex)
		if err != nil {
			return nil, errors.New("invalid graph row")
		}
		row := make([]string, result.GetColSize())
		for colIndex := range row {
			value, err := record.GetValueByIndex(colIndex)
			if err != nil {
				return nil, errors.New("invalid graph value")
			}
			switch {
			case value.IsString():
				text, err := value.AsString()
				if err != nil {
					return nil, errors.New("invalid graph string")
				}
				encoded, err := json.Marshal(text)
				if err != nil {
					return nil, err
				}
				row[colIndex] = string(encoded)
			case value.IsInt():
				integer, err := value.AsInt()
				if err != nil {
					return nil, errors.New("invalid graph integer")
				}
				row[colIndex] = strconv.FormatInt(integer, 10)
			case value.IsNull():
				null, err := value.AsNull()
				if err != nil || null != nebulatype.NullType___NULL__ {
					return nil, errors.New("graph returned an invalid NULL type")
				}
				row[colIndex] = "null"
			default:
				return nil, errors.New("unexpected graph property type")
			}
		}
		table[rowIndex+1] = row
	}
	return table, nil
}

func persistenceNullableText(encoded string) (*string, error) {
	if encoded == "NULL" || encoded == "null" || encoded == "__NULL__" {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal([]byte(encoded), &value); err != nil {
		return nil, errors.New("persisted interface property is not a string or NULL")
	}
	return &value, nil
}

func persistenceString(value string) string {
	var decoded string
	if json.Unmarshal([]byte(value), &decoded) == nil {
		return decoded
	}
	return value
}
func persistenceTime(value string) (time.Time, error) {
	value = persistenceString(value)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02 15:04:05.999999"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid persisted graph timestamp")
}
