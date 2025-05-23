// Copyright 2024 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integrationtests

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pingcap/tidb/pkg/disttask/framework/proto"
	"github.com/pingcap/tidb/pkg/disttask/framework/scheduler"
	"github.com/pingcap/tidb/pkg/disttask/framework/storage"
	"github.com/pingcap/tidb/pkg/disttask/framework/testutil"
	"github.com/pingcap/tidb/pkg/testkit"
	"github.com/pingcap/tidb/pkg/testkit/testfailpoint"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/exp/rand"
)

func getMockBasicSchedulerExtForScope(ctrl *gomock.Controller, subtaskCnt int) scheduler.Extension {
	return testutil.GetMockSchedulerExt(ctrl, testutil.SchedulerInfo{
		AllErrorRetryable: true,
		StepInfos: []testutil.StepInfo{
			{Step: proto.StepOne, SubtaskCnt: subtaskCnt},
			{Step: proto.StepTwo, SubtaskCnt: 1},
		},
	})
}

func submitTaskAndCheckSuccessForScope(ctx context.Context, t *testing.T, taskKey string, nodeCnt int, targetScope string, testContext *testutil.TestContext) int64 {
	return submitTaskAndCheckSuccess(ctx, t, taskKey, targetScope, testContext, map[proto.Step]int{
		proto.StepOne: nodeCnt,
		proto.StepTwo: 1,
	})
}

func checkSubtaskOnNodes(ctx context.Context, t *testing.T, taskID int64, expectedNodes []string) {
	mgr, err := storage.GetTaskManager()
	require.NoError(t, err)
	nodes, err := testutil.GetSubtaskNodes(ctx, mgr, taskID)
	require.NoError(t, err)
	slices.Sort(nodes)
	slices.Sort(expectedNodes)
	require.EqualValues(t, expectedNodes, nodes)
}

func TestScopeBasic(t *testing.T) {
	nodeCnt := 3
	c := testutil.NewTestDXFContext(t, nodeCnt, 16, true)

	registerExampleTask(t, c.MockCtrl, getMockBasicSchedulerExtForScope(c.MockCtrl, 3), c.TestContext, nil)
	tk := testkit.NewTestKit(t, c.Store)

	// 1. all "" role.
	taskID := submitTaskAndCheckSuccessForScope(c.Ctx, t, "😁", nodeCnt, "", c.TestContext)

	checkSubtaskOnNodes(c.Ctx, t, taskID, []string{":4000", ":4001", ":4002"})
	tk.MustQuery(`select role from mysql.dist_framework_meta where host=":4000"`).Check(testkit.Rows(""))
	tk.MustQuery(`select role from mysql.dist_framework_meta where host=":4001"`).Check(testkit.Rows(""))
	tk.MustQuery(`select role from mysql.dist_framework_meta where host=":4002"`).Check(testkit.Rows(""))

	// 2. one "background" role.
	tk.MustExec("set global tidb_service_scope=background")
	tk.MustQuery("select @@global.tidb_service_scope").Check(testkit.Rows("background"))
	tk.MustQuery("select @@tidb_service_scope").Check(testkit.Rows("background"))

	ch := make(chan struct{})
	var counter atomic.Int32
	testfailpoint.EnableCall(t, "github.com/pingcap/tidb/pkg/disttask/framework/scheduler/syncRefresh", func() {
		if counter.Add(1) == 1 {
			ch <- struct{}{}
		}
	})
	<-ch
	taskID = submitTaskAndCheckSuccessForScope(c.Ctx, t, "😊", nodeCnt, "background", c.TestContext)

	tk.MustQuery(`select role from mysql.dist_framework_meta where host=":4000"`).Check(testkit.Rows("background"))
	tk.MustQuery(`select role from mysql.dist_framework_meta where host=":4001"`).Check(testkit.Rows(""))
	tk.MustQuery(`select role from mysql.dist_framework_meta where host=":4002"`).Check(testkit.Rows(""))
	checkSubtaskOnNodes(c.Ctx, t, taskID, []string{":4000"})

	// 3. 2 "background" role.
	tk.MustExec("update mysql.dist_framework_meta set role = \"background\" where host = \":4001\"")
	time.Sleep(5 * time.Second)
	ch2 := make(chan struct{})
	var counter2 atomic.Int32
	testfailpoint.EnableCall(t, "github.com/pingcap/tidb/pkg/disttask/framework/scheduler/syncRefresh", func() {
		if counter2.Add(1) == 1 {
			ch2 <- struct{}{}
		}
	})
	<-ch2
	taskID = submitTaskAndCheckSuccessForScope(c.Ctx, t, "😆", nodeCnt, "background", c.TestContext)
	checkSubtaskOnNodes(c.Ctx, t, taskID, []string{":4000", ":4001"})
	tk.MustQuery(`select role from mysql.dist_framework_meta where host=":4000"`).Check(testkit.Rows("background"))
	tk.MustQuery(`select role from mysql.dist_framework_meta where host=":4001"`).Check(testkit.Rows("background"))
	tk.MustQuery(`select role from mysql.dist_framework_meta where host=":4002"`).Check(testkit.Rows(""))
}

func TestSetScope(t *testing.T) {
	store := testkit.CreateMockStore(t)
	tk := testkit.NewTestKit(t, store)

	defer func() {
		tk.MustExec(`set global tidb_service_scope=""`)
	}()
	// 1. set rand sys var.
	tk.MustExec("set global tidb_service_scope=rand")
	// 2. set keyspace id.
	tk.MustExec("update mysql.dist_framework_meta set keyspace_id = 16777216 where host = \":4000\"")
	tk.MustQuery("select keyspace_id from mysql.dist_framework_meta where host = \":4000\"").Check(testkit.Rows("16777216"))
}

type targetScopeCase struct {
	scope      string
	nodeScopes []string
}

func generateScopeCase(nodeCnt int, scopeCnt int) targetScopeCase {
	seed := time.Now().Unix()
	rand.Seed(uint64(seed))
	scope := fmt.Sprintf("scope-%d", rand.Intn(100))

	nodeScopes := make([]string, nodeCnt)
	for i := range nodeCnt - scopeCnt {
		nodeScopes[i] = fmt.Sprintf("scope-%d", rand.Intn(100))
	}
	for i := range scopeCnt {
		nodeScopes[nodeCnt-scopeCnt+i] = scope
	}

	return targetScopeCase{
		scope:      scope,
		nodeScopes: nodeScopes,
	}
}

func runTargetScopeCase(t *testing.T, c *testutil.TestDXFContext, tk *testkit.TestKit, testCase targetScopeCase, idx int, nodeCnt int) {
	for i := range testCase.nodeScopes {
		tk.MustExec(fmt.Sprintf("update mysql.dist_framework_meta set role = \"%s\" where host = \"%s\"", testCase.nodeScopes[i], c.GetNodeIDByIdx(i)))
	}
	ch := make(chan struct{})
	var counter atomic.Int32
	testfailpoint.EnableCall(t, "github.com/pingcap/tidb/pkg/disttask/framework/scheduler/syncRefresh", func() {
		if counter.Add(1) <= 3 {
			ch <- struct{}{}
		}
	})
	<-ch
	<-ch
	<-ch
	taskID := submitTaskAndCheckSuccessForScope(c.Ctx, t, "task"+strconv.Itoa(idx), nodeCnt, testCase.scope, c.TestContext)
	expected := make([]string, 0)
	for i, scope := range testCase.nodeScopes {
		if scope == testCase.scope {
			expected = append(expected, c.GetNodeIDByIdx(i))
		}
	}
	checkSubtaskOnNodes(c.Ctx, t, taskID, expected)
}

func TestTargetScope(t *testing.T) {
	nodeCnt := 10
	c := testutil.NewTestDXFContext(t, nodeCnt, 16, true)
	registerExampleTask(t, c.MockCtrl, getMockBasicSchedulerExtForScope(c.MockCtrl, nodeCnt), c.TestContext, nil)
	tk := testkit.NewTestKit(t, c.Store)
	caseNum := 10
	for i := range caseNum {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			runTargetScopeCase(t, c, tk, generateScopeCase(nodeCnt, 5), i, nodeCnt)
		})
	}
}

func TestTiDBMaxDistTaskNodesSettings(t *testing.T) {
	store := testkit.CreateMockStore(t)
	tk := testkit.NewTestKit(t, store)

	tk.MustExec(`set global tidb_max_dist_task_nodes=-1`)
	tk.MustQuery("select @@global.tidb_max_dist_task_nodes").Check(testkit.Rows("-1"))
	tk.MustExec(`set global tidb_max_dist_task_nodes=1`)
	tk.MustQuery("select @@global.tidb_max_dist_task_nodes").Check(testkit.Rows("1"))
	tk.MustExec(`set global tidb_max_dist_task_nodes=128`)
	tk.MustQuery("select @@global.tidb_max_dist_task_nodes").Check(testkit.Rows("128"))

	tk.MustGetErrMsg(`set global tidb_max_dist_task_nodes=0`, "max_dist_task_nodes should be -1 or [1, 128]")
	tk.MustExec(`set global tidb_max_dist_task_nodes=129`)
	tk.MustQuery("select @@global.tidb_max_dist_task_nodes").Check(testkit.Rows("128"))
}
