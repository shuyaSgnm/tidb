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

package structure

import (
	"bytes"

	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/util/codec"
)

// TypeFlag is for data structure meta/data flag.
type TypeFlag byte

const (
	// StringMeta is the flag for string meta.
	StringMeta TypeFlag = 'S'
	// StringData is the flag for string data.
	StringData TypeFlag = 's'
	// HashMeta is the flag for hash meta.
	HashMeta TypeFlag = 'H'
	// HashData is the flag for hash data.
	HashData TypeFlag = 'h'
	// ListMeta is the flag for list meta.
	ListMeta TypeFlag = 'L'
	// ListData is the flag for list data.
	ListData TypeFlag = 'l'
)

// Make linter happy, since encodeHashMetaKey is unused in this repo.
var _ = (&TxStructure{}).encodeHashMetaKey

// EncodeStringDataKey will encode string key.
func (t *TxStructure) EncodeStringDataKey(key []byte) kv.Key {
	// for codec Encode, we may add extra bytes data, so here and following encode
	// we will use extra length like 4 for a little optimization.
	ek := make([]byte, 0, len(t.prefix)+len(key)+24)
	ek = append(ek, t.prefix...)
	ek = codec.EncodeBytes(ek, key)
	return codec.EncodeUint(ek, uint64(StringData))
}

func (t *TxStructure) decodeStringDataKey(ek kv.Key) ([]byte, error) {
	var (
		key []byte
		err error
		tp  uint64
	)

	if !bytes.HasPrefix(ek, t.prefix) {
		return nil, errors.New("invalid encoded hash data key prefix")
	}

	ek = ek[len(t.prefix):]

	ek, key, err = codec.DecodeBytes(ek, nil)
	if err != nil {
		return nil, errors.Trace(err)
	}

	ek, tp, err = codec.DecodeUint(ek)
	if err != nil {
		return nil, errors.Trace(err)
	} else if TypeFlag(tp) != StringData {
		return nil, ErrInvalidHashKeyFlag.GenWithStack("invalid encoded string data key flag %c", byte(tp))
	}

	return key, errors.Trace(err)
}

// nolint:unused
func (t *TxStructure) encodeHashMetaKey(key []byte) kv.Key {
	ek := make([]byte, 0, len(t.prefix)+codec.EncodedBytesLength(len(key))+8)
	ek = append(ek, t.prefix...)
	ek = codec.EncodeBytes(ek, key)
	return codec.EncodeUint(ek, uint64(HashMeta))
}

func (t *TxStructure) encodeHashDataKey(key []byte, field []byte) kv.Key {
	ek := make([]byte, 0, len(t.prefix)+codec.EncodedBytesLength(len(key))+8+codec.EncodedBytesLength(len(field)))
	ek = append(ek, t.prefix...)
	ek = codec.EncodeBytes(ek, key)
	ek = codec.EncodeUint(ek, uint64(HashData))
	return codec.EncodeBytes(ek, field)
}

// EncodeHashDataKey exports for tests.
func (t *TxStructure) EncodeHashDataKey(key []byte, field []byte) kv.Key {
	return t.encodeHashDataKey(key, field)
}

func (t *TxStructure) decodeHashDataKey(ek kv.Key) (key, field []byte, err error) {
	var tp uint64

	if !bytes.HasPrefix(ek, t.prefix) {
		return nil, nil, errors.New("invalid encoded hash data key prefix")
	}

	ek = ek[len(t.prefix):]

	ek, key, err = codec.DecodeBytes(ek, nil)
	if err != nil {
		return nil, nil, errors.Trace(err)
	}

	ek, tp, err = codec.DecodeUint(ek)
	if err != nil {
		return nil, nil, errors.Trace(err)
	} else if TypeFlag(tp) != HashData {
		return nil, nil, ErrInvalidHashKeyFlag.GenWithStack("invalid encoded hash data key flag %c", byte(tp))
	}

	_, field, err = codec.DecodeBytes(ek, nil)
	return key, field, errors.Trace(err)
}

func (t *TxStructure) hashDataKeyPrefix(key []byte) kv.Key {
	ek := make([]byte, 0, len(t.prefix)+len(key)+24)
	ek = append(ek, t.prefix...)
	ek = codec.EncodeBytes(ek, key)
	return codec.EncodeUint(ek, uint64(HashData))
}

func (t *TxStructure) encodeListMetaKey(key []byte) kv.Key {
	ek := make([]byte, 0, len(t.prefix)+len(key)+24)
	ek = append(ek, t.prefix...)
	ek = codec.EncodeBytes(ek, key)
	return codec.EncodeUint(ek, uint64(ListMeta))
}

func (t *TxStructure) encodeListDataKey(key []byte, index int64) kv.Key {
	ek := make([]byte, 0, len(t.prefix)+len(key)+36)
	ek = append(ek, t.prefix...)
	ek = codec.EncodeBytes(ek, key)
	ek = codec.EncodeUint(ek, uint64(ListData))
	return codec.EncodeInt(ek, index)
}
