package migration

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func mockMySQL(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db, mock
}

func TestMySQLRequiresAdvisoryLockBeforeSchemaWrites(t *testing.T) {
	db, mock := mockMySQL(t)
	mock.ExpectQuery("SELECT DATABASE").WillReturnRows(sqlmock.NewRows([]string{"database"}).AddRow("isolated"))
	mock.ExpectQuery("SELECT GET_LOCK").WithArgs(sqlmock.AnyArg(), 30).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(0))
	require.ErrorContains(t, Apply(context.Background(), db), "migration lock")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMySQLRecordsPartialDDLFailureBeforeReleasingLock(t *testing.T) {
	db, mock := mockMySQL(t)
	mock.ExpectQuery("SELECT DATABASE").WillReturnRows(sqlmock.NewRows([]string{"database"}).AddRow("isolated"))
	mock.ExpectQuery("SELECT GET_LOCK").WithArgs(sqlmock.AnyArg(), 30).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(1))
	mock.ExpectQuery("SELECT @@SESSION.autocommit").WillReturnRows(sqlmock.NewRows([]string{"autocommit"}).AddRow(1))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS topology_schema_migrations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT version, name, checksum, state FROM topology_schema_migrations ORDER BY version").WillReturnRows(sqlmock.NewRows([]string{"version", "name", "checksum", "state"}))
	mock.ExpectExec("INSERT INTO topology_schema_migrations").WithArgs(0, "0000_ledger.sql", sqlmock.AnyArg(), "applied", sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO topology_schema_migrations").WithArgs(1, "0001_inventory.sql", sqlmock.AnyArg(), "dirty", sqlmock.AnyArg(), nil).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("CREATE TABLE topology_scopes").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE sources").WillReturnError(errors.New("simulated DDL failure"))
	mock.ExpectExec("UPDATE topology_schema_migrations SET state = 'failed'").WithArgs("statement 2 failed", 1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT RELEASE_LOCK").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"released"}).AddRow(1))
	err := Apply(context.Background(), db)
	require.ErrorContains(t, err, "simulated DDL failure")
	require.ErrorContains(t, err, "manual recovery")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMySQLRecordsCanceledStatement(t *testing.T) {
	db, mock := mockMySQL(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mock.ExpectQuery("SELECT DATABASE").WillReturnRows(sqlmock.NewRows([]string{"database"}).AddRow("isolated"))
	mock.ExpectQuery("SELECT GET_LOCK").WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(1))
	mock.ExpectQuery("SELECT @@SESSION.autocommit").WillReturnRows(sqlmock.NewRows([]string{"autocommit"}).AddRow(1))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS topology_schema_migrations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT version, name, checksum, state FROM topology_schema_migrations ORDER BY version").WillReturnRows(sqlmock.NewRows([]string{"version", "name", "checksum", "state"}))
	mock.ExpectExec("INSERT INTO topology_schema_migrations").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO topology_schema_migrations").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("CREATE TABLE topology_scopes").WillReturnError(context.Canceled)
	mock.ExpectExec("UPDATE topology_schema_migrations SET state = 'failed'").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT RELEASE_LOCK").WillReturnRows(sqlmock.NewRows([]string{"released"}).AddRow(1))
	require.ErrorIs(t, Apply(ctx, db), context.Canceled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMySQLRejectsNonAutocommitSession(t *testing.T) {
	db, mock := mockMySQL(t)
	mock.ExpectQuery("SELECT DATABASE").WillReturnRows(sqlmock.NewRows([]string{"database"}).AddRow("isolated"))
	mock.ExpectQuery("SELECT GET_LOCK").WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(1))
	mock.ExpectQuery("SELECT @@SESSION.autocommit").WillReturnRows(sqlmock.NewRows([]string{"autocommit"}).AddRow(0))
	mock.ExpectQuery("SELECT RELEASE_LOCK").WillReturnRows(sqlmock.NewRows([]string{"released"}).AddRow(1))
	require.ErrorContains(t, Apply(context.Background(), db), "autocommit")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMySQLAppliesCurrentGraphUpgradeWithChecksumLedger(t *testing.T) {
	db, mock := mockMySQL(t)
	steps, err := loadSteps("mysql")
	require.NoError(t, err)
	prior := sqlmock.NewRows([]string{"version", "name", "checksum", "state"})
	for _, step := range steps[:2] {
		prior.AddRow(step.version, step.name, step.checksum, "applied")
	}
	mock.ExpectQuery("SELECT DATABASE").WillReturnRows(sqlmock.NewRows([]string{"database"}).AddRow("isolated"))
	mock.ExpectQuery("SELECT GET_LOCK").WithArgs(sqlmock.AnyArg(), 30).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(1))
	mock.ExpectQuery("SELECT @@SESSION.autocommit").WillReturnRows(sqlmock.NewRows([]string{"autocommit"}).AddRow(1))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS topology_schema_migrations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT version, name, checksum, state FROM topology_schema_migrations ORDER BY version").WillReturnRows(prior)
	mock.ExpectExec("INSERT INTO topology_schema_migrations").WithArgs(2, "0002_current_graph.sql", steps[2].checksum, "dirty", sqlmock.AnyArg(), nil).WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec(regexp.QuoteMeta(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(steps[2].sql), ";")))).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE topology_schema_migrations SET state = 'applied'").WithArgs(sqlmock.AnyArg(), 2).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT RELEASE_LOCK").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"released"}).AddRow(1))
	require.NoError(t, Apply(context.Background(), db))
	require.NoError(t, mock.ExpectationsWereMet())
}
