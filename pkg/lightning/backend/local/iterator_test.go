// Copyright 2021 PingCAP, Inc.
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

package local

import (
	"bytes"
	"fmt"
	"math/rand"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/pingcap/tidb/pkg/lightning/common"
	"github.com/pingcap/tidb/pkg/lightning/log"
	"github.com/pingcap/tidb/pkg/lightning/membuf"
	"github.com/stretchr/testify/require"
)

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func TestDupDetectIterator(t *testing.T) {
	pairs := make([]common.KvPair, 0, 20)
	prevRowMax := int64(0)
	// Unique pairs.
	for range 20 {
		pairs = append(pairs, common.KvPair{
			Key:   randBytes(32),
			Val:   randBytes(128),
			RowID: common.EncodeIntRowID(prevRowMax),
		})
		prevRowMax++
	}
	// Duplicate pairs which repeat the same key twice.
	for i := 20; i < 40; i++ {
		key := randBytes(32)
		pairs = append(pairs, common.KvPair{
			Key:   key,
			Val:   randBytes(128),
			RowID: common.EncodeIntRowID(prevRowMax),
		})
		prevRowMax++
		pairs = append(pairs, common.KvPair{
			Key:   key,
			Val:   randBytes(128),
			RowID: common.EncodeIntRowID(prevRowMax),
		})
		prevRowMax++
	}
	// Duplicate pairs which repeat the same key three times.
	for i := 40; i < 50; i++ {
		key := randBytes(32)
		pairs = append(pairs, common.KvPair{
			Key:   key,
			Val:   randBytes(128),
			RowID: common.EncodeIntRowID(prevRowMax),
		})
		prevRowMax++
		pairs = append(pairs, common.KvPair{
			Key:   key,
			Val:   randBytes(128),
			RowID: common.EncodeIntRowID(prevRowMax),
		})
		prevRowMax++
		pairs = append(pairs, common.KvPair{
			Key:   key,
			Val:   randBytes(128),
			RowID: common.EncodeIntRowID(prevRowMax),
		})
		prevRowMax++
	}

	// Find duplicates from the generated pairs.
	var dupPairs []common.KvPair
	sort.Slice(pairs, func(i, j int) bool {
		return bytes.Compare(pairs[i].Key, pairs[j].Key) < 0
	})
	uniqueKeys := make([][]byte, 0)
	for i := 0; i < len(pairs); {
		j := i + 1
		for j < len(pairs) && bytes.Equal(pairs[j-1].Key, pairs[j].Key) {
			j++
		}
		uniqueKeys = append(uniqueKeys, pairs[i].Key)
		if i+1 == j {
			i++
			continue
		}
		for k := i; k < j; k++ {
			dupPairs = append(dupPairs, pairs[k])
		}
		i = j
	}

	keyAdapter := common.DupDetectKeyAdapter{}

	// Write pairs to db after shuffling the pairs.
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	rnd.Shuffle(len(pairs), func(i, j int) {
		pairs[i], pairs[j] = pairs[j], pairs[i]
	})
	storeDir := t.TempDir()
	db, err := pebble.Open(filepath.Join(storeDir, "kv"), &pebble.Options{})
	require.NoError(t, err)
	wb := db.NewBatch()
	for _, p := range pairs {
		key := keyAdapter.Encode(nil, p.Key, p.RowID)
		require.NoError(t, wb.Set(key, p.Val, nil))
	}
	require.NoError(t, wb.Commit(pebble.Sync))

	dupDB, err := pebble.Open(filepath.Join(storeDir, "duplicates"), &pebble.Options{})
	require.NoError(t, err)
	pool := membuf.NewPool()
	defer pool.Destroy()
	iter := newDupDetectIter(db, keyAdapter, &pebble.IterOptions{}, dupDB, log.L(), common.DupDetectOpt{}, pool.NewBuffer())
	sort.Slice(pairs, func(i, j int) bool {
		key1 := keyAdapter.Encode(nil, pairs[i].Key, pairs[i].RowID)
		key2 := keyAdapter.Encode(nil, pairs[j].Key, pairs[j].RowID)
		return bytes.Compare(key1, key2) < 0
	})

	// Verify first pair.
	require.True(t, iter.First())
	require.True(t, iter.Valid())
	require.Equal(t, pairs[0].Key, iter.Key())
	require.Equal(t, pairs[0].Val, iter.Value())

	// Verify last pair.
	require.True(t, iter.Last())
	require.True(t, iter.Valid())
	require.Equal(t, pairs[len(pairs)-1].Key, iter.Key())
	require.Equal(t, pairs[len(pairs)-1].Val, iter.Value())

	// Iterate all keys and check the count of unique keys.
	for iter.First(); iter.Valid(); iter.Next() {
		require.Equal(t, uniqueKeys[0], iter.Key())
		uniqueKeys = uniqueKeys[1:]
	}
	require.NoError(t, iter.Error())
	require.Equal(t, 0, len(uniqueKeys))
	require.NoError(t, iter.Close())
	require.NoError(t, db.Close())

	// Check duplicates detected by dupDetectIter.
	iter2 := newDupDBIter(dupDB, keyAdapter, &pebble.IterOptions{})
	var detectedPairs []common.KvPair
	for iter2.First(); iter2.Valid(); iter2.Next() {
		detectedPairs = append(detectedPairs, common.KvPair{
			Key: slices.Clone(iter2.Key()),
			Val: slices.Clone(iter2.Value()),
		})
	}
	require.NoError(t, iter2.Error())
	require.NoError(t, iter2.Close())
	require.NoError(t, dupDB.Close())
	require.Equal(t, len(dupPairs), len(detectedPairs))

	sort.Slice(dupPairs, func(i, j int) bool {
		keyCmp := bytes.Compare(dupPairs[i].Key, dupPairs[j].Key)
		return keyCmp < 0 || keyCmp == 0 && bytes.Compare(dupPairs[i].Val, dupPairs[j].Val) < 0
	})
	sort.Slice(detectedPairs, func(i, j int) bool {
		keyCmp := bytes.Compare(detectedPairs[i].Key, detectedPairs[j].Key)
		return keyCmp < 0 || keyCmp == 0 && bytes.Compare(detectedPairs[i].Val, detectedPairs[j].Val) < 0
	})
	for i := range detectedPairs {
		require.Equal(t, dupPairs[i].Key, detectedPairs[i].Key)
		require.Equal(t, dupPairs[i].Val, detectedPairs[i].Val)
	}
}

func TestKeyAdapterEncoding(t *testing.T) {
	keyAdapter := common.DupDetectKeyAdapter{}
	srcKey := []byte{1, 2, 3}
	v := keyAdapter.Encode(nil, srcKey, common.EncodeIntRowID(1))
	resKey, err := keyAdapter.Decode(nil, v)
	require.NoError(t, err)
	require.EqualValues(t, srcKey, resKey)

	v = keyAdapter.Encode(nil, srcKey, []byte("mock_common_handle"))
	resKey, err = keyAdapter.Decode(nil, v)
	require.NoError(t, err)
	require.EqualValues(t, srcKey, resKey)
}

func BenchmarkDupDetectIter(b *testing.B) {
	keyAdapter := common.DupDetectKeyAdapter{}
	db, _ := pebble.Open(filepath.Join(b.TempDir(), "kv"), &pebble.Options{})
	wb := db.NewBatch()
	val := []byte("value")
	for i := range 100_000 {
		keyNum := i
		// mimic we have 20% duplication
		if keyNum%5 == 0 {
			keyNum--
		}
		keyStr := fmt.Sprintf("%09d", keyNum)
		rowID := strconv.Itoa(i)
		key := keyAdapter.Encode(nil, []byte(keyStr), []byte(rowID))
		wb.Set(key, val, nil)
	}
	wb.Commit(pebble.Sync)

	pool := membuf.NewPool()
	dupDB, _ := pebble.Open(filepath.Join(b.TempDir(), "dup"), &pebble.Options{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		iter := newDupDetectIter(
			db,
			keyAdapter,
			&pebble.IterOptions{},
			dupDB,
			log.L(),
			common.DupDetectOpt{},
			pool.NewBuffer(),
		)
		keyCnt := 0
		for iter.First(); iter.Valid(); iter.Next() {
			keyCnt++
		}
		iter.Close()
	}
}
