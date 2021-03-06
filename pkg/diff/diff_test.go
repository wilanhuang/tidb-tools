// Copyright 2018 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package diff

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	. "github.com/pingcap/check"
	"github.com/pingcap/tidb-tools/pkg/dbutil"
	"github.com/pingcap/tidb-tools/pkg/importer"
)

func TestClient(t *testing.T) {
	TestingT(t)
}

var _ = Suite(&testDiffSuite{})

type testDiffSuite struct{}

func (*testDiffSuite) TestGenerateSQLs(c *C) {
	createTableSQL := "CREATE TABLE `test`.`atest` (`id` int(24), `name` varchar(24), `birthday` datetime, `update_time` time, `money` decimal(20,2), primary key(`id`))"
	tableInfo, err := dbutil.GetTableInfoBySQL(createTableSQL)
	c.Assert(err, IsNil)

	rowsData := map[string][]byte{
		"id":          []byte("1"),
		"name":        []byte("xxx"),
		"birthday":    []byte("2018-01-01 00:00:00"),
		"update_time": []byte("10:10:10"),
		"money":       []byte("11.1111"),
	}
	null := map[string]bool{
		"id":          false,
		"name":        false,
		"birthday":    false,
		"update_time": false,
		"money":       false,
	}
	_, orderKeyCols := dbutil.SelectUniqueOrderKey(tableInfo)
	replaceSQL := generateDML("replace", rowsData, null, orderKeyCols, tableInfo, "test")
	deleteSQL := generateDML("delete", rowsData, null, orderKeyCols, tableInfo, "test")
	c.Assert(replaceSQL, Equals, "REPLACE INTO `test`.`atest`(`id`,`name`,`birthday`,`update_time`,`money`) VALUES (1,'xxx','2018-01-01 00:00:00','10:10:10',11.1111);")
	c.Assert(deleteSQL, Equals, "DELETE FROM `test`.`atest` WHERE `id` = 1;")

	// test the unique key
	createTableSQL2 := "CREATE TABLE `test`.`atest` (`id` int(24), `name` varchar(24), `birthday` datetime, `update_time` time, `money` decimal(20,2), unique key(`id`, `name`))"
	tableInfo2, err := dbutil.GetTableInfoBySQL(createTableSQL2)
	c.Assert(err, IsNil)
	_, orderKeyCols2 := dbutil.SelectUniqueOrderKey(tableInfo2)
	replaceSQL = generateDML("replace", rowsData, null, orderKeyCols2, tableInfo2, "test")
	deleteSQL = generateDML("delete", rowsData, null, orderKeyCols2, tableInfo2, "test")
	c.Assert(replaceSQL, Equals, "REPLACE INTO `test`.`atest`(`id`,`name`,`birthday`,`update_time`,`money`) VALUES (1,'xxx','2018-01-01 00:00:00','10:10:10',11.1111);")
	c.Assert(deleteSQL, Equals, "DELETE FROM `test`.`atest` WHERE `id` = 1 AND `name` = 'xxx';")

	// test value is nil
	rowsData["name"] = []byte("")
	null["name"] = true
	replaceSQL = generateDML("replace", rowsData, null, orderKeyCols, tableInfo, "test")
	deleteSQL = generateDML("delete", rowsData, null, orderKeyCols, tableInfo, "test")
	c.Assert(replaceSQL, Equals, "REPLACE INTO `test`.`atest`(`id`,`name`,`birthday`,`update_time`,`money`) VALUES (1,NULL,'2018-01-01 00:00:00','10:10:10',11.1111);")
	c.Assert(deleteSQL, Equals, "DELETE FROM `test`.`atest` WHERE `id` = 1;")

	rowsData["id"] = []byte("")
	null["id"] = true
	replaceSQL = generateDML("replace", rowsData, null, orderKeyCols, tableInfo, "test")
	deleteSQL = generateDML("delete", rowsData, null, orderKeyCols, tableInfo, "test")
	c.Assert(replaceSQL, Equals, "REPLACE INTO `test`.`atest`(`id`,`name`,`birthday`,`update_time`,`money`) VALUES (NULL,NULL,'2018-01-01 00:00:00','10:10:10',11.1111);")
	c.Assert(deleteSQL, Equals, "DELETE FROM `test`.`atest` WHERE `id` is NULL;")
}

func (t *testDiffSuite) TestDiff(c *C) {
	dbConn, err := createConn()
	c.Assert(err, IsNil)

	_, err = dbConn.Query("create database if not exists test")
	c.Assert(err, IsNil)

	testStructEqual(dbConn, c)
	testDataEqual(dbConn, c)
}

func testStructEqual(conn *sql.DB, c *C) {
	testCases := []struct {
		createSourceTable string
		createTargetTable string
		dropSourceTable   string
		dropTargetTable   string
		structEqual       bool
	}{
		{
			"CREATE TABLE `test`.`testa`(`id` int, `name` varchar(24))",
			"CREATE TABLE `test`.`testb`(`id` int, `name` varchar(24), unique key (`id`))",
			"DROP TABLE `test`.`testa`",
			"DROP TABLE `test`.`testb`",
			false,
		}, {
			"CREATE TABLE `test`.`testa`(`id` int, `name` varchar(24))",
			"CREATE TABLE `test`.`testb`(`id` int, `name2` varchar(24))",
			"DROP TABLE `test`.`testa`",
			"DROP TABLE `test`.`testb`",
			false,
		}, {
			"CREATE TABLE `test`.`testa`(`id` int, `name` varchar(24))",
			"CREATE TABLE `test`.`testb`(`id` int)",
			"DROP TABLE `test`.`testa`",
			"DROP TABLE `test`.`testb`",
			false,
		}, {
			"CREATE TABLE `test`.`testa`(`id` int, `name` varchar(24))",
			"CREATE TABLE `test`.`testb`(`id` int, `name` varchar(24))",
			"DROP TABLE `test`.`testa`",
			"DROP TABLE `test`.`testb`",
			true,
		}, {
			"CREATE TABLE `test`.`testa`(`id` int, `name` varchar(24))",
			"CREATE TABLE `test`.`testb`(`id` varchar(24), name varchar(24))",
			"DROP TABLE `test`.`testa`",
			"DROP TABLE `test`.`testb`",
			false,
		},
	}

	for _, testCase := range testCases {
		_, err := conn.Query(testCase.createSourceTable)
		c.Assert(err, IsNil)
		_, err = conn.Query(testCase.createTargetTable)
		c.Assert(err, IsNil)

		tableDiff := createTableDiff(conn)
		structEqual, _, err := tableDiff.Equal(context.Background(), func(sql string) error {
			fmt.Println(sql)
			return nil
		})
		c.Assert(structEqual, Equals, testCase.structEqual)

		_, err = conn.Query(testCase.dropSourceTable)
		c.Assert(err, IsNil)
		_, err = conn.Query(testCase.dropTargetTable)
		c.Assert(err, IsNil)
	}
}

func testDataEqual(dbConn *sql.DB, c *C) {
	sourceTable := "testa"
	targetTable := "testb"

	defer func() {
		_, _ = dbConn.Query(fmt.Sprintf("drop table test.%s", sourceTable))
		_, _ = dbConn.Query(fmt.Sprintf("drop table test.%s", targetTable))
	}()

	err := generateData(dbConn, dbutil.GetDBConfigFromEnv("test"), sourceTable, targetTable)
	c.Assert(err, IsNil)

	// compare data, should be equal
	fixSqls := make([]string, 0, 10)
	writeSqls := func(sql string) error {
		fixSqls = append(fixSqls, sql)
		return nil
	}

	tableDiff := createTableDiff(dbConn)
	structEqual, dataEqual, err := tableDiff.Equal(context.Background(), writeSqls)
	c.Assert(err, IsNil)
	c.Assert(structEqual, Equals, true)
	c.Assert(dataEqual, Equals, true)

	// update data and then compare data, dataEqual should be false
	err = updateData(dbConn, targetTable)
	c.Assert(err, IsNil)

	structEqual, dataEqual, err = tableDiff.Equal(context.Background(), writeSqls)
	c.Assert(err, IsNil)
	c.Assert(structEqual, Equals, true)
	c.Assert(dataEqual, Equals, false)

	// use fixSqls to fix data, and then compare data
	for _, sql := range fixSqls {
		_, err = dbConn.Exec(sql)
		c.Assert(err, IsNil)
	}
	structEqual, dataEqual, err = tableDiff.Equal(context.Background(), writeSqls)
	c.Assert(err, IsNil)
	c.Assert(structEqual, Equals, true)
	c.Assert(dataEqual, Equals, true)
}

func createTableDiff(db *sql.DB) *TableDiff {
	sourceTableInstance := &TableInstance{
		Conn:   db,
		Schema: "test",
		Table:  "testa",
	}

	targetTableInstance := &TableInstance{
		Conn:   db,
		Schema: "test",
		Table:  "testb",
	}

	return &TableDiff{
		SourceTables: []*TableInstance{sourceTableInstance},
		TargetTable:  targetTableInstance,
	}
}

func createConn() (*sql.DB, error) {
	return dbutil.OpenDB(dbutil.GetDBConfigFromEnv("test"))
}

func generateData(dbConn *sql.DB, dbCfg dbutil.DBConfig, sourceTable, targetTable string) error {
	createTableSQL := fmt.Sprintf(`CREATE TABLE test.%s (
		a date NOT NULL,
		b datetime DEFAULT NULL,
		c time DEFAULT NULL,
		d varchar(10) COLLATE latin1_bin DEFAULT NULL,
		e int(10) DEFAULT NULL,
		h year(4) DEFAULT NULL,
		PRIMARY KEY (a))`, sourceTable)

	cfg := &importer.Config{
		TableSQL:    createTableSQL,
		WorkerCount: 1,
		JobCount:    10000,
		Batch:       100,
		DBCfg:       dbCfg,
	}

	// generate data for source table
	importer.DoProcess(cfg)

	// generate data for target table
	_, err := dbConn.Query(fmt.Sprintf("create table test.%s like test.%s", targetTable, sourceTable))
	if err != nil {
		return err
	}

	_, err = dbConn.Query(fmt.Sprintf("insert into test.%s (a, b, c, d, e, h) select a, b, c, d, e, h from test.%s", targetTable, sourceTable))
	if err != nil {
		return err
	}

	return nil
}

func updateData(dbConn *sql.DB, table string) error {
	values, err := dbutil.GetRandomValues(context.Background(), dbConn, "test", table, "e", 3, math.MinInt64, math.MaxInt64, "true", "")
	if err != nil {
		return err
	}

	_, err = dbConn.Exec(fmt.Sprintf("update test.%s set e = e+1 where e = %v", table, values[0]))
	if err != nil {
		return err
	}

	_, err = dbConn.Exec(fmt.Sprintf("delete from test.%s where e = %v", table, values[1]))
	if err != nil {
		return err
	}

	_, err = dbConn.Exec(fmt.Sprintf("replace into test.%s values('1992-09-27','2018-09-03 16:26:27','14:45:33','i',2048790075,2008)", table))
	if err != nil {
		return err
	}

	return nil
}
