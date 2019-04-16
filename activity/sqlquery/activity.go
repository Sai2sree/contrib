package sqlquery

import (
	"database/sql"
	"fmt"
	"github.com/project-flogo/core/support/log"
	"reflect"

	"github.com/project-flogo/core/activity"
	"github.com/project-flogo/core/data/metadata"
)

func init() {
	_ = activity.Register(&Activity{}, New)
}

const (
	ovResults = "results"
)

var activityMd = activity.ToMetadata(&Settings{}, &Input{}, &Output{})

func New(ctx activity.InitContext) (activity.Activity, error) {
	s := &Settings{MaxIdleConns: 2}
	err := metadata.MapToStruct(ctx.Settings(), s, true)
	if err != nil {
		return nil, err
	}

	dbType, err := ToDbType(s.DbType)
	if err != nil {
		return nil, err
	}

	ctx.Logger().Debugf("DB: '%s'", s.DbType)

	// todo move this to a shared connection object
	db, err := getConnection(s)
	if err != nil {
		return nil, err
	}

	sqlStatement, err := NewSQLStatement(dbType, s.Query)
	if err != nil {
		return nil, err
	}

	if sqlStatement.Type() != StSelect {
		return nil, fmt.Errorf("only select statement is supported")
	}

	act := &Activity{db: db, sqlStatement: sqlStatement}

	if !s.DisablePrepared {
		ctx.Logger().Debugf("Using PreparedStatement: %s", sqlStatement.PreparedStatementSQL())
		act.stmt, err = db.Prepare(sqlStatement.PreparedStatementSQL())
		if err != nil {
			return nil, err
		}
	}

	return act, nil
}

// Activity is a Counter Activity implementation
type Activity struct {
	db           *sql.DB
	sqlStatement *SQLStatement
	stmt         *sql.Stmt
	labeledResults bool
}

// Metadata implements activity.Activity.Metadata
func (a *Activity) Metadata() *activity.Metadata {
	return activityMd
}

func (a *Activity) Cleanup() error {
	if a.stmt != nil {
		err := a.stmt.Close()
		log.RootLogger().Warnf("error cleaning up SQL Query activity: %v", err)
	}

	log.RootLogger().Tracef("cleaning up SQL Query activity")

	return a.db.Close()
}

// Eval implements activity.Activity.Eval
func (a *Activity) Eval(ctx activity.Context) (done bool, err error) {

	in := &Input{}
	err = ctx.GetInputObject(in)
	if err != nil {
		return false, err
	}

	results, err := a.doSelect(in.Params)
	if err != nil {
		return false, err
	}

	err = ctx.SetOutput(ovResults, results)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (a *Activity) doSelect(params map[string]interface{}) (interface{}, error) {

	var err error
	var rows *sql.Rows

	if a.stmt != nil {
		args := a.sqlStatement.GetPreparedStatementArgs(params)
		rows, err = a.stmt.Query(args...)
	} else {
		rows, err = a.db.Query(a.sqlStatement.ToStatementSQL(params))
	}
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var results interface{}

	if a.labeledResults {
		results, err = getLabeledResults(rows)
	} else {
		results, err = getResults(rows)
	}

	if err != nil {
		return nil, err
	}

	return results, nil
}

func getLabeledResults(rows *sql.Rows) ([]map[string]interface{},error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []map[string]interface{}

	for rows.Next() {

		values := make([]interface{}, len(columns))
		for i := range values {
			values[i] = new(interface{})
		}

		err = rows.Scan(values...)
		if err != nil {
			return nil, err
		}

		resMap := make(map[string]interface{}, len(columns))
		for i, column := range columns {
			resMap[column] = *(values[i].(*interface{}))
		}

		//todo do we need to do column mapping

		results = append(results, resMap)
	}

	return results, rows.Err()
}

func getResults(rows *sql.Rows) ([][]interface{},error) {

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	cTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	fmt.Printf("ctypes: %v", cTypes)

	types := make([]reflect.Type, len(cTypes))
	for i, tp := range cTypes {
		st := tp.ScanType()
		if st == nil {
			continue
		}
		types[i] = st
	}

	var results [][]interface{}

	for rows.Next() {

		values := make([]interface{}, len(columns))
		for i := range values {
			values[i] = reflect.New(types[i]).Interface()
		}
		//values := make([]interface{}, len(columns))
		//for i := range values {
		//	values[i] = new(interface{})
		//}

		err = rows.Scan(values...)
		if err != nil {
			return nil, err
		}

		for _, value := range values {
			if v,ok:=value.([]byte); ok  {
				fmt.Println("val:", string(v))
			}
		}

		r:= *sql.RawBytes{}


		//todo do we need to do column type mapping

		results = append(results, values)
	}

	return results, rows.Err()
}

//todo move to shared connection
func getConnection(s *Settings) (*sql.DB, error) {

	db, err := sql.Open(s.DriverName, s.DataSourceName)
	if err != nil {
		return nil, err
	}

	if s.MaxOpenConns > 0 {
		db.SetMaxOpenConns(s.MaxOpenConns)
	}

	if s.MaxIdleConns != 2 {
		db.SetMaxIdleConns(s.MaxIdleConns)
	}

	return db, err
}
