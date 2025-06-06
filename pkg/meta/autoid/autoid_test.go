// Copyright 2015 PingCAP, Inc.
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

package autoid_test

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pingcap/errors"
	"github.com/pingcap/failpoint"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta"
	"github.com/pingcap/tidb/pkg/meta/autoid"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/store/mockstore"
	"github.com/pingcap/tidb/pkg/util"
	"github.com/stretchr/testify/require"
	"github.com/tikv/client-go/v2/tikv"
)

type mockRequirement struct {
	kv.Storage
}

func (r mockRequirement) Store() kv.Storage {
	return r.Storage
}

func (r mockRequirement) AutoIDClient() *autoid.ClientDiscover {
	return nil
}

func TestSignedAutoid(t *testing.T) {
	require.NoError(t, failpoint.Enable("github.com/pingcap/tidb/pkg/meta/autoid/mockAutoIDChange", `return(true)`))
	defer func() {
		require.NoError(t, failpoint.Disable("github.com/pingcap/tidb/pkg/meta/autoid/mockAutoIDChange"))
	}()

	store, err := mockstore.NewMockStore(mockstore.WithStoreType(mockstore.EmbedUnistore))
	require.NoError(t, err)
	defer func() {
		err := store.Close()
		require.NoError(t, err)
	}()

	ctx := kv.WithInternalSourceType(context.Background(), kv.InternalTxnMeta)
	err = kv.RunInNewTxn(ctx, store, false, func(ctx context.Context, txn kv.Transaction) error {
		m := meta.NewMutator(txn)
		err = m.CreateDatabase(&model.DBInfo{ID: 1, Name: ast.NewCIStr("a")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 1, Name: ast.NewCIStr("t")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 2, Name: ast.NewCIStr("t1")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 3, Name: ast.NewCIStr("t1")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 4, Name: ast.NewCIStr("t2")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 5, Name: ast.NewCIStr("t3")})
		require.NoError(t, err)
		return nil
	})
	require.NoError(t, err)

	// Since the test here is applicable to any type of allocators, autoid.RowIDAllocType is chosen.
	alloc := autoid.NewAllocator(mockRequirement{store}, 1, 1, false, autoid.RowIDAllocType)
	require.NotNil(t, alloc)

	globalAutoID, err := alloc.NextGlobalAutoID()
	require.NoError(t, err)
	require.Equal(t, int64(1), globalAutoID)
	_, id, err := alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), id)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), id)
	globalAutoID, err = alloc.NextGlobalAutoID()
	require.NoError(t, err)
	require.Equal(t, autoid.GetStep()+1, globalAutoID)

	// rebase
	err = alloc.Rebase(context.Background(), int64(1), true)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(3), id)
	err = alloc.Rebase(context.Background(), int64(3), true)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(4), id)
	err = alloc.Rebase(context.Background(), int64(10), true)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(11), id)
	err = alloc.Rebase(context.Background(), int64(3010), true)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(3011), id)

	alloc = autoid.NewAllocator(mockRequirement{store}, 1, 1, false, autoid.RowIDAllocType)
	require.NotNil(t, alloc)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, autoid.GetStep()+1, id)

	alloc = autoid.NewAllocator(mockRequirement{store}, 1, 2, false, autoid.RowIDAllocType)
	require.NotNil(t, alloc)
	err = alloc.Rebase(context.Background(), int64(1), false)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), id)

	alloc = autoid.NewAllocator(mockRequirement{store}, 1, 3, false, autoid.RowIDAllocType)
	require.NotNil(t, alloc)
	err = alloc.Rebase(context.Background(), int64(3210), false)
	require.NoError(t, err)
	alloc = autoid.NewAllocator(mockRequirement{store}, 1, 3, false, autoid.RowIDAllocType)
	require.NotNil(t, alloc)
	err = alloc.Rebase(context.Background(), int64(3000), false)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(3211), id)
	err = alloc.Rebase(context.Background(), int64(6543), false)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(6544), id)

	// Test the MaxInt64 is the upper bound of `alloc` function but not `rebase`.
	err = alloc.Rebase(context.Background(), int64(math.MaxInt64-1), true)
	require.NoError(t, err)
	_, _, err = alloc.Alloc(ctx, 1, 1, 1)
	require.Error(t, err)
	err = alloc.Rebase(context.Background(), int64(math.MaxInt64), true)
	require.NoError(t, err)

	// alloc N for signed
	alloc = autoid.NewAllocator(mockRequirement{store}, 1, 4, false, autoid.RowIDAllocType)
	require.NotNil(t, alloc)
	globalAutoID, err = alloc.NextGlobalAutoID()
	require.NoError(t, err)
	require.Equal(t, int64(1), globalAutoID)
	minv, maxv, err := alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), maxv-minv)
	require.Equal(t, int64(1), minv+1)

	minv, maxv, err = alloc.Alloc(ctx, 2, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), maxv-minv)
	require.Equal(t, int64(2), minv+1)
	require.Equal(t, int64(3), maxv)

	minv, maxv, err = alloc.Alloc(ctx, 100, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(100), maxv-minv)
	expected := int64(4)
	for i := minv + 1; i <= maxv; i++ {
		require.Equal(t, expected, i)
		expected++
	}

	err = alloc.Rebase(context.Background(), int64(1000), false)
	require.NoError(t, err)
	minv, maxv, err = alloc.Alloc(ctx, 3, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(3), maxv-minv)
	require.Equal(t, int64(1001), minv+1)
	require.Equal(t, int64(1002), minv+2)
	require.Equal(t, int64(1003), maxv)

	lastRemainOne := alloc.End()
	err = alloc.Rebase(context.Background(), alloc.End()-2, false)
	require.NoError(t, err)
	minv, maxv, err = alloc.Alloc(ctx, 5, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(5), maxv-minv)
	require.Greater(t, minv+1, lastRemainOne)

	// Test for increment & offset for signed.
	alloc = autoid.NewAllocator(mockRequirement{store}, 1, 5, false, autoid.RowIDAllocType)
	require.NotNil(t, alloc)

	increment := int64(2)
	offset := int64(100)
	require.NoError(t, err)
	require.Equal(t, int64(1), globalAutoID)
	minv, maxv, err = alloc.Alloc(ctx, 1, increment, offset)
	require.NoError(t, err)
	require.Equal(t, int64(99), minv)
	require.Equal(t, int64(100), maxv)

	minv, maxv, err = alloc.Alloc(ctx, 2, increment, offset)
	require.NoError(t, err)
	require.Equal(t, int64(4), maxv-minv)
	require.Equal(t, autoid.CalcNeededBatchSize(100, 2, increment, offset, false), maxv-minv)
	require.Equal(t, int64(100), minv)
	require.Equal(t, int64(104), maxv)

	increment = int64(5)
	minv, maxv, err = alloc.Alloc(ctx, 3, increment, offset)
	require.NoError(t, err)
	require.Equal(t, int64(11), maxv-minv)
	require.Equal(t, autoid.CalcNeededBatchSize(104, 3, increment, offset, false), maxv-minv)
	require.Equal(t, int64(104), minv)
	require.Equal(t, int64(115), maxv)
	firstID := autoid.SeekToFirstAutoIDSigned(104, increment, offset)
	require.Equal(t, int64(105), firstID)

	increment = int64(15)
	minv, maxv, err = alloc.Alloc(ctx, 2, increment, offset)
	require.NoError(t, err)
	require.Equal(t, int64(30), maxv-minv)
	require.Equal(t, autoid.CalcNeededBatchSize(115, 2, increment, offset, false), maxv-minv)
	require.Equal(t, int64(115), minv)
	require.Equal(t, int64(145), maxv)
	firstID = autoid.SeekToFirstAutoIDSigned(115, increment, offset)
	require.Equal(t, int64(130), firstID)

	offset = int64(200)
	minv, maxv, err = alloc.Alloc(ctx, 2, increment, offset)
	require.NoError(t, err)
	require.Equal(t, int64(16), maxv-minv)
	// offset-1 > base will cause alloc rebase to offset-1.
	require.Equal(t, autoid.CalcNeededBatchSize(offset-1, 2, increment, offset, false), maxv-minv)
	require.Equal(t, int64(199), minv)
	require.Equal(t, int64(215), maxv)
	firstID = autoid.SeekToFirstAutoIDSigned(offset-1, increment, offset)
	require.Equal(t, int64(200), firstID)
}

func TestUnsignedAutoid(t *testing.T) {
	require.NoError(t, failpoint.Enable("github.com/pingcap/tidb/pkg/meta/autoid/mockAutoIDChange", `return(true)`))
	defer func() {
		require.NoError(t, failpoint.Disable("github.com/pingcap/tidb/pkg/meta/autoid/mockAutoIDChange"))
	}()

	store, err := mockstore.NewMockStore(mockstore.WithStoreType(mockstore.EmbedUnistore))
	require.NoError(t, err)
	defer func() {
		err := store.Close()
		require.NoError(t, err)
	}()

	ctx := kv.WithInternalSourceType(context.Background(), kv.InternalTxnMeta)
	err = kv.RunInNewTxn(ctx, store, false, func(ctx context.Context, txn kv.Transaction) error {
		m := meta.NewMutator(txn)
		err = m.CreateDatabase(&model.DBInfo{ID: 1, Name: ast.NewCIStr("a")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 1, Name: ast.NewCIStr("t")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 2, Name: ast.NewCIStr("t1")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 3, Name: ast.NewCIStr("t1")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 4, Name: ast.NewCIStr("t2")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 5, Name: ast.NewCIStr("t3")})
		require.NoError(t, err)
		return nil
	})
	require.NoError(t, err)

	alloc := autoid.NewAllocator(mockRequirement{store}, 1, 1, true, autoid.RowIDAllocType)
	require.NotNil(t, alloc)

	globalAutoID, err := alloc.NextGlobalAutoID()
	require.NoError(t, err)
	require.Equal(t, int64(1), globalAutoID)
	_, id, err := alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), id)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), id)
	globalAutoID, err = alloc.NextGlobalAutoID()
	require.NoError(t, err)
	require.Equal(t, autoid.GetStep()+1, globalAutoID)

	// rebase
	err = alloc.Rebase(context.Background(), int64(1), true)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(3), id)
	err = alloc.Rebase(context.Background(), int64(3), true)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(4), id)
	err = alloc.Rebase(context.Background(), int64(10), true)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(11), id)
	err = alloc.Rebase(context.Background(), int64(3010), true)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(3011), id)

	alloc = autoid.NewAllocator(mockRequirement{store}, 1, 1, true, autoid.RowIDAllocType)
	require.NotNil(t, alloc)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, autoid.GetStep()+1, id)

	alloc = autoid.NewAllocator(mockRequirement{store}, 1, 2, true, autoid.RowIDAllocType)
	require.NotNil(t, alloc)
	err = alloc.Rebase(context.Background(), int64(1), false)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), id)

	alloc = autoid.NewAllocator(mockRequirement{store}, 1, 3, true, autoid.RowIDAllocType)
	require.NotNil(t, alloc)
	err = alloc.Rebase(context.Background(), int64(3210), false)
	require.NoError(t, err)
	alloc = autoid.NewAllocator(mockRequirement{store}, 1, 3, true, autoid.RowIDAllocType)
	require.NotNil(t, alloc)
	err = alloc.Rebase(context.Background(), int64(3000), false)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(3211), id)
	err = alloc.Rebase(context.Background(), int64(6543), false)
	require.NoError(t, err)
	_, id, err = alloc.Alloc(ctx, 1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(6544), id)

	// Test the MaxUint64 is the upper bound of `alloc` func but not `rebase`.
	// This looks weird, but it's the mysql behaviour.
	// For example, in MySQL, CREATE TABLE t1 (pk BIGINT UNSIGNED AUTO_INCREMENT, PRIMARY KEY (pk));
	// 	INSERT INTO t1 VALUES (18446744073709551615-1);   -- rebase to maxinum-1 success
	// 	INSERT INTO t1 VALUES ();  -- the next alloc fail, cannot allocate 18446744073709551615
	// 	INSERT INTO t1 VALUES (18446744073709551615);   -- but directly rebase to maxinum success
	var n uint64 = math.MaxUint64 - 1
	un := int64(n)
	err = alloc.Rebase(context.Background(), un, true)
	require.NoError(t, err)
	_, _, err = alloc.Alloc(ctx, 1, 1, 1)
	require.Error(t, err)
	un = int64(n + 1)
	err = alloc.Rebase(context.Background(), un, true)
	require.NoError(t, err)

	// alloc N for unsigned
	alloc = autoid.NewAllocator(mockRequirement{store}, 1, 4, true, autoid.RowIDAllocType)
	require.NotNil(t, alloc)
	globalAutoID, err = alloc.NextGlobalAutoID()
	require.NoError(t, err)
	require.Equal(t, int64(1), globalAutoID)

	minv, maxv, err := alloc.Alloc(ctx, 2, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), maxv-minv)
	require.Equal(t, int64(1), minv+1)
	require.Equal(t, int64(2), maxv)

	err = alloc.Rebase(context.Background(), int64(500), true)
	require.NoError(t, err)
	minv, maxv, err = alloc.Alloc(ctx, 2, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), maxv-minv)
	require.Equal(t, int64(501), minv+1)
	require.Equal(t, int64(502), maxv)

	lastRemainOne := alloc.End()
	err = alloc.Rebase(context.Background(), alloc.End()-2, false)
	require.NoError(t, err)
	minv, maxv, err = alloc.Alloc(ctx, 5, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(5), maxv-minv)
	require.Greater(t, minv+1, lastRemainOne)

	// Test increment & offset for unsigned. Using AutoRandomType to avoid valid range check for increment and offset.
	alloc = autoid.NewAllocator(mockRequirement{store}, 1, 5, true, autoid.AutoRandomType)
	require.NotNil(t, alloc)
	require.NoError(t, err)
	require.Equal(t, int64(1), globalAutoID)

	increment := int64(2)
	n = math.MaxUint64 - 100
	offset := int64(n)

	minv, maxv, err = alloc.Alloc(ctx, 2, increment, offset)
	require.NoError(t, err)
	require.Equal(t, uint64(math.MaxUint64-101), uint64(minv))
	require.Equal(t, uint64(math.MaxUint64-98), uint64(maxv))

	require.Equal(t, autoid.CalcNeededBatchSize(int64(uint64(offset)-1), 2, increment, offset, true), maxv-minv)
	firstID := autoid.SeekToFirstAutoIDUnSigned(uint64(minv), uint64(increment), uint64(offset))
	require.Equal(t, uint64(math.MaxUint64-100), firstID)
}

// TestConcurrentAlloc is used for the test that
// multiple allocators allocate ID with the same table ID concurrently.
func TestConcurrentAlloc(t *testing.T) {
	store, err := mockstore.NewMockStore(mockstore.WithStoreType(mockstore.EmbedUnistore))
	require.NoError(t, err)
	defer func() {
		err := store.Close()
		require.NoError(t, err)
	}()
	autoid.SetStep(100)
	defer func() {
		autoid.SetStep(5000)
	}()

	dbID := int64(2)
	tblID := int64(100)
	ctx := kv.WithInternalSourceType(context.Background(), kv.InternalTxnMeta)
	err = kv.RunInNewTxn(ctx, store, false, func(ctx context.Context, txn kv.Transaction) error {
		m := meta.NewMutator(txn)
		err = m.CreateDatabase(&model.DBInfo{ID: dbID, Name: ast.NewCIStr("a")})
		require.NoError(t, err)
		err = m.CreateTableOrView(dbID, &model.TableInfo{ID: tblID, Name: ast.NewCIStr("t")})
		require.NoError(t, err)
		return nil
	})
	require.NoError(t, err)

	var mu sync.Mutex
	var wg util.WaitGroupWrapper
	m := map[int64]struct{}{}
	count := 10
	errCh := make(chan error, count)

	allocIDs := func() {
		ctx := context.Background()
		alloc := autoid.NewAllocator(mockRequirement{store}, dbID, tblID, false, autoid.RowIDAllocType)
		for range int(autoid.GetStep()) + 5 {
			_, id, err1 := alloc.Alloc(ctx, 1, 1, 1)
			if err1 != nil {
				errCh <- err1
				break
			}

			mu.Lock()
			if _, ok := m[id]; ok {
				errCh <- fmt.Errorf("duplicate id:%v", id)
				mu.Unlock()
				break
			}
			m[id] = struct{}{}
			mu.Unlock()

			// test Alloc N
			N := rand.Uint64() % 100
			minv, maxv, err1 := alloc.Alloc(ctx, N, 1, 1)
			if err1 != nil {
				errCh <- err1
				break
			}

			errFlag := false
			mu.Lock()
			for i := minv + 1; i <= maxv; i++ {
				if _, ok := m[i]; ok {
					errCh <- fmt.Errorf("duplicate id:%v", i)
					errFlag = true
					mu.Unlock()
					break
				}
				m[i] = struct{}{}
			}
			if errFlag {
				break
			}
			mu.Unlock()
		}
	}
	for range count {
		num := 1
		wg.Run(func() {
			time.Sleep(time.Duration(num%10) * time.Microsecond)
			allocIDs()
		})
	}
	wg.Wait()

	close(errCh)
	err = <-errCh
	require.NoError(t, err)
}

// TestRollbackAlloc tests that when the allocation transaction commit failed,
// the local variable base and end doesn't change.
func TestRollbackAlloc(t *testing.T) {
	store, err := mockstore.NewMockStore(mockstore.WithStoreType(mockstore.EmbedUnistore))
	require.NoError(t, err)
	defer func() {
		err := store.Close()
		require.NoError(t, err)
	}()
	dbID := int64(1)
	tblID := int64(2)
	ctx := kv.WithInternalSourceType(context.Background(), kv.InternalTxnMeta)
	err = kv.RunInNewTxn(ctx, store, false, func(ctx context.Context, txn kv.Transaction) error {
		m := meta.NewMutator(txn)
		err = m.CreateDatabase(&model.DBInfo{ID: dbID, Name: ast.NewCIStr("a")})
		require.NoError(t, err)
		err = m.CreateTableOrView(dbID, &model.TableInfo{ID: tblID, Name: ast.NewCIStr("t")})
		require.NoError(t, err)
		return nil
	})
	require.NoError(t, err)

	injectConf := new(kv.InjectionConfig)
	injectConf.SetCommitError(errors.New("injected"))
	injectedStore := kv.NewInjectedStore(store, injectConf)
	alloc := autoid.NewAllocator(mockRequirement{injectedStore}, 1, 2, false, autoid.RowIDAllocType)
	_, _, err = alloc.Alloc(ctx, 1, 1, 1)
	require.Error(t, err)
	require.Equal(t, int64(0), alloc.Base())
	require.Equal(t, int64(0), alloc.End())

	err = alloc.Rebase(context.Background(), 100, true)
	require.Error(t, err)
	require.Equal(t, int64(0), alloc.Base())
	require.Equal(t, int64(0), alloc.End())
}

// TestNextStep tests generate next auto id step.
func TestNextStep(t *testing.T) {
	nextStep := autoid.NextStep(2000000, 1*time.Nanosecond)
	require.Equal(t, int64(2000000), nextStep)
	nextStep = autoid.NextStep(678910, 10*time.Second)
	require.Equal(t, int64(678910), nextStep)
	nextStep = autoid.NextStep(50000, 10*time.Minute)
	require.Equal(t, int64(30000), nextStep)
}

// Fix a computation logic bug in allocator computation.
func TestAllocComputationIssue(t *testing.T) {
	require.NoError(t, failpoint.Enable("github.com/pingcap/tidb/pkg/meta/autoid/mockAutoIDCustomize", `return(true)`))
	defer func() {
		require.NoError(t, failpoint.Disable("github.com/pingcap/tidb/pkg/meta/autoid/mockAutoIDCustomize"))
	}()

	store, err := mockstore.NewMockStore(mockstore.WithStoreType(mockstore.EmbedUnistore))
	require.NoError(t, err)
	defer func() {
		err := store.Close()
		require.NoError(t, err)
	}()

	ctx := kv.WithInternalSourceType(context.Background(), kv.InternalTxnMeta)
	err = kv.RunInNewTxn(ctx, store, false, func(ctx context.Context, txn kv.Transaction) error {
		m := meta.NewMutator(txn)
		err = m.CreateDatabase(&model.DBInfo{ID: 1, Name: ast.NewCIStr("a")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 1, Name: ast.NewCIStr("t")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 2, Name: ast.NewCIStr("t1")})
		require.NoError(t, err)
		return nil
	})
	require.NoError(t, err)

	// Since the test here is applicable to any type of allocators, autoid.RowIDAllocType is chosen.
	unsignedAlloc1 := autoid.NewAllocator(mockRequirement{store}, 1, 1, true, autoid.RowIDAllocType)
	require.NotNil(t, unsignedAlloc1)
	signedAlloc1 := autoid.NewAllocator(mockRequirement{store}, 1, 1, false, autoid.RowIDAllocType)
	require.NotNil(t, signedAlloc1)
	signedAlloc2 := autoid.NewAllocator(mockRequirement{store}, 1, 2, false, autoid.RowIDAllocType)
	require.NotNil(t, signedAlloc2)

	// the next valid two value must be 13 & 16, batch size = 6.
	err = unsignedAlloc1.Rebase(context.Background(), 10, false)
	require.NoError(t, err)
	// the next valid two value must be 10 & 13, batch size = 6.
	err = signedAlloc2.Rebase(context.Background(), 7, false)
	require.NoError(t, err)
	// Simulate the rest cache is not enough for next batch, assuming 10 & 13, batch size = 4.
	autoid.TestModifyBaseAndEndInjection(unsignedAlloc1, 9, 9)
	// Simulate the rest cache is not enough for next batch, assuming 10 & 13, batch size = 4.
	autoid.TestModifyBaseAndEndInjection(signedAlloc1, 4, 6)

	// Here will recompute the new allocator batch size base on new base = 10, which will get 6.
	minv, maxv, err := unsignedAlloc1.Alloc(ctx, 2, 3, 1)
	require.NoError(t, err)
	require.Equal(t, int64(10), minv)
	require.Equal(t, int64(16), maxv)
	minv, maxv, err = signedAlloc2.Alloc(ctx, 2, 3, 1)
	require.NoError(t, err)
	require.Equal(t, int64(7), minv)
	require.Equal(t, int64(13), maxv)
}

func TestIssue40584(t *testing.T) {
	store, err := mockstore.NewMockStore(mockstore.WithStoreType(mockstore.EmbedUnistore))
	require.NoError(t, err)
	defer func() {
		err := store.Close()
		require.NoError(t, err)
	}()

	ctx := kv.WithInternalSourceType(context.Background(), kv.InternalTxnMeta)
	err = kv.RunInNewTxn(ctx, store, false, func(ctx context.Context, txn kv.Transaction) error {
		m := meta.NewMutator(txn)
		err = m.CreateDatabase(&model.DBInfo{ID: 1, Name: ast.NewCIStr("a")})
		require.NoError(t, err)
		err = m.CreateTableOrView(1, &model.TableInfo{ID: 1, Name: ast.NewCIStr("t")})
		require.NoError(t, err)
		return nil
	})
	require.NoError(t, err)

	alloc := autoid.NewAllocator(mockRequirement{store}, 1, 1, false, autoid.RowIDAllocType)
	require.NotNil(t, alloc)

	finishAlloc := make(chan bool)
	finishBase := make(chan bool)
	var done int32 = 0

	// call allocator.Alloc and allocator.Base in parallel for 3 seconds to detect data race
	go func() {
		for {
			alloc.Alloc(ctx, 1, 1, 1)
			if atomic.LoadInt32(&done) > 0 {
				break
			}
		}
		finishAlloc <- true
	}()

	go func() {
		for {
			alloc.Base()
			if atomic.LoadInt32(&done) > 0 {
				break
			}
		}
		finishBase <- true
	}()

	runTime := time.NewTimer(time.Second * 3)
	<-runTime.C
	atomic.AddInt32(&done, 1)
	<-finishAlloc
	<-finishBase
}

func TestGetAutoIDServiceLeaderEtcdPath(t *testing.T) {
	// keyspaceID = tikv.NullspaceID means tidb not set keyspace.
	keyspaceID := tikv.NullspaceID
	path := autoid.GetAutoIDServiceLeaderEtcdPath(uint32(keyspaceID))
	require.Equal(t, autoid.AutoIDLeaderPath, path)

	// In keyspace scenario, assume the keyspaceID=1, the actually etcd key like /keyspaces/tidb/1/tidb/autoid/leader,
	// The keyspace prefix `/keyspaces/tidb/1` is already in the domain
	// when initializing etcdclient by setting the etcd namespace. Is added to the top of the key.
	// So we need to put a forward slash at the beginning of the path.
	keyspaceID = 1
	path = autoid.GetAutoIDServiceLeaderEtcdPath(uint32(keyspaceID))
	require.Equal(t, "/"+autoid.AutoIDLeaderPath, path)
}
