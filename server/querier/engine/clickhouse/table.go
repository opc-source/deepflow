/*
 * Copyright (c) 2024 Yunshan Networks
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package clickhouse

import (
	"context"

	"golang.org/x/exp/slices"

	"github.com/deepflowio/deepflow/server/querier/common"
	"github.com/deepflowio/deepflow/server/querier/engine/clickhouse/client"
	chCommon "github.com/deepflowio/deepflow/server/querier/engine/clickhouse/common"
)

func GetDatabases() *common.Result {
	var values []interface{}
	for db := range chCommon.DB_TABLE_MAP {
		values = append(values, []string{db})
	}
	return &common.Result{
		Columns: []interface{}{"name"},
		Values:  values,
	}
}

func GetTables(db, queryCacheTTL, orgID string, useQueryCache bool, ctx context.Context, DebugInfo *client.DebugInfo) *common.Result {
	var values []interface{}
	tables, ok := chCommon.DB_TABLE_MAP[db]
	if !ok {
		return nil
	}
	if slices.Contains([]string{chCommon.DB_NAME_DEEPFLOW_ADMIN, chCommon.DB_NAME_EXT_METRICS, chCommon.DB_NAME_DEEPFLOW_TENANT}, db) {
		values = append(values, chCommon.GetExtTables(db, queryCacheTTL, orgID, useQueryCache, ctx, DebugInfo)...)
	} else if db == chCommon.DB_NAME_PROMETHEUS {
		values = append(values, chCommon.GetPrometheusTables(db, queryCacheTTL, orgID, useQueryCache, ctx, DebugInfo)...)
	} else {
		for _, table := range tables {
			datasource, err := chCommon.GetDatasources(db, table, orgID)
			if err != nil {
				log.Error(err)
			}
			values = append(values, []interface{}{table, datasource})
		}
	}
	return &common.Result{
		Columns: []interface{}{"name", "datasources"},
		Values:  values,
	}
}
