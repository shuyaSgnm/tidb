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

// This files only expose some functions for testing propose, it should not be used in production code.
// So we use `//go:build !codes` to exclude this file in production code.

//go:build !codes

package mock

// NewContext creates a new mocked sessionctx.Context.
// This function should only be used for testing.
// Avoid using this when you are in a context with a `kv.Storage` instance, especially when you are going to access
// the data in it. Consider using testkit.NewSession(t, store) instead when possible.
func NewContext() *Context {
	return newContext()
}
