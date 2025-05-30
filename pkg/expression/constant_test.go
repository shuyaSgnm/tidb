// Copyright 2016 PingCAP, Inc.
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

package expression

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression/exprctx"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/cascades/base"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/mock"
	"github.com/stretchr/testify/require"
)

func newColumn(id int) *Column {
	return newColumnWithType(id, types.NewFieldType(mysql.TypeLonglong))
}

func newColumnWithType(id int, t *types.FieldType) *Column {
	return &Column{
		UniqueID: int64(id),
		RetType:  t,
	}
}

func newLonglong(value int64) *Constant {
	return &Constant{
		Value:   types.NewIntDatum(value),
		RetType: types.NewFieldType(mysql.TypeLonglong),
	}
}

func newString(value string, collation string) *Constant {
	return &Constant{
		Value:   types.NewStringDatum(value),
		RetType: types.NewFieldTypeWithCollation(mysql.TypeVarchar, collation, 255),
	}
}

func newFunctionWithMockCtx(funcName string, args ...Expression) Expression {
	return newFunction(mock.NewContext(), funcName, args...)
}

func newFunction(ctx BuildContext, funcName string, args ...Expression) Expression {
	return newFunctionWithType(ctx, funcName, types.NewFieldType(mysql.TypeLonglong), args...)
}

func newFunctionWithType(ctx BuildContext, funcName string, tp *types.FieldType, args ...Expression) Expression {
	return NewFunctionInternal(ctx, funcName, tp, args...)
}

func TestConstantPropagation(t *testing.T) {
	tests := []struct {
		solver     []PropagateConstantSolver
		conditions []Expression
		result     string
	}{
		{
			solver: []PropagateConstantSolver{newPropConstSolver()},
			conditions: []Expression{
				newFunctionWithMockCtx(ast.EQ, newColumn(0), newColumn(1)),
				newFunctionWithMockCtx(ast.EQ, newColumn(1), newColumn(2)),
				newFunctionWithMockCtx(ast.EQ, newColumn(2), newColumn(3)),
				newFunctionWithMockCtx(ast.EQ, newColumn(3), newLonglong(1)),
				newFunctionWithMockCtx(ast.LogicOr, newLonglong(1), newColumn(0)),
			},
			result: "1, eq(Column#0, 1), eq(Column#1, 1), eq(Column#2, 1), eq(Column#3, 1)",
		},
		{
			solver: []PropagateConstantSolver{newPropConstSolver()},
			conditions: []Expression{
				newFunctionWithMockCtx(ast.EQ, newColumn(0), newColumn(1)),
				newFunctionWithMockCtx(ast.EQ, newColumn(1), newLonglong(1)),
				newFunctionWithMockCtx(ast.NE, newColumn(2), newLonglong(2)),
			},
			result: "eq(Column#0, 1), eq(Column#1, 1), ne(Column#2, 2)",
		},
		{
			solver: []PropagateConstantSolver{newPropConstSolver()},
			conditions: []Expression{
				newFunctionWithMockCtx(ast.EQ, newColumn(0), newColumn(1)),
				newFunctionWithMockCtx(ast.EQ, newColumn(1), newLonglong(1)),
				newFunctionWithMockCtx(ast.EQ, newColumn(2), newColumn(3)),
				newFunctionWithMockCtx(ast.GE, newColumn(2), newLonglong(2)),
				newFunctionWithMockCtx(ast.NE, newColumn(2), newLonglong(4)),
				newFunctionWithMockCtx(ast.NE, newColumn(3), newLonglong(5)),
			},
			result: "eq(Column#0, 1), eq(Column#1, 1), eq(Column#2, Column#3), ge(Column#2, 2), ge(Column#3, 2), ne(Column#2, 4), ne(Column#2, 5), ne(Column#3, 4), ne(Column#3, 5)",
		},
		{
			solver: []PropagateConstantSolver{newPropConstSolver()},
			conditions: []Expression{
				newFunctionWithMockCtx(ast.EQ, newColumn(0), newColumn(1)),
				newFunctionWithMockCtx(ast.EQ, newColumn(0), newColumn(2)),
				newFunctionWithMockCtx(ast.GE, newColumn(1), newLonglong(0)),
			},
			result: "eq(Column#0, Column#1), eq(Column#0, Column#2), ge(Column#0, 0), ge(Column#1, 0), ge(Column#2, 0)",
		},
		{
			solver: []PropagateConstantSolver{newPropConstSolver()},
			conditions: []Expression{
				newFunctionWithMockCtx(ast.EQ, newColumn(0), newColumn(1)),
				newFunctionWithMockCtx(ast.GT, newColumn(0), newLonglong(2)),
				newFunctionWithMockCtx(ast.GT, newColumn(1), newLonglong(3)),
				newFunctionWithMockCtx(ast.LT, newColumn(0), newLonglong(1)),
				newFunctionWithMockCtx(ast.GT, newLonglong(2), newColumn(1)),
			},
			result: "eq(Column#0, Column#1), gt(2, Column#0), gt(2, Column#1), gt(Column#0, 2), gt(Column#0, 3), gt(Column#1, 2), gt(Column#1, 3), lt(Column#0, 1), lt(Column#1, 1)",
		},
		{
			solver: []PropagateConstantSolver{newPropConstSolver()},
			conditions: []Expression{
				newFunctionWithMockCtx(ast.EQ, newLonglong(1), newColumn(0)),
				newLonglong(0),
			},
			result: "0",
		},
		{
			solver: []PropagateConstantSolver{newPropConstSolver()},
			conditions: []Expression{
				newFunctionWithMockCtx(ast.EQ, newColumn(0), newColumn(1)),
				newFunctionWithMockCtx(ast.In, newColumn(0), newLonglong(1), newLonglong(2)),
				newFunctionWithMockCtx(ast.In, newColumn(1), newLonglong(3), newLonglong(4)),
			},
			result: "eq(Column#0, Column#1), in(Column#0, 1, 2), in(Column#0, 3, 4), in(Column#1, 1, 2), in(Column#1, 3, 4)",
		},
		{
			solver: []PropagateConstantSolver{newPropConstSolver()},
			conditions: []Expression{
				newFunctionWithMockCtx(ast.EQ, newColumn(0), newColumn(1)),
				newFunctionWithMockCtx(ast.EQ, newColumn(0), newFunctionWithMockCtx(ast.BitLength, newColumn(2))),
			},
			result: "eq(Column#0, Column#1), eq(Column#0, bit_length(cast(Column#2, var_string(20)))), eq(Column#1, bit_length(cast(Column#2, var_string(20))))",
		},
		{
			solver: []PropagateConstantSolver{newPropConstSolver()},
			conditions: []Expression{
				newFunctionWithMockCtx(ast.EQ, newColumn(0), newColumn(1)),
				newFunctionWithMockCtx(ast.LE, newFunctionWithMockCtx(ast.Mul, newColumn(0), newColumn(0)), newLonglong(50)),
			},
			result: "eq(Column#0, Column#1), le(mul(Column#0, Column#0), 50), le(mul(Column#1, Column#1), 50)",
		},
		{
			solver: []PropagateConstantSolver{newPropConstSolver()},
			conditions: []Expression{
				newFunctionWithMockCtx(ast.EQ, newColumn(0), newColumn(1)),
				newFunctionWithMockCtx(ast.LE, newColumn(0), newFunctionWithMockCtx(ast.Plus, newColumn(1), newLonglong(1))),
			},
			result: "eq(Column#0, Column#1), le(Column#0, plus(Column#0, 1)), le(Column#0, plus(Column#1, 1)), le(Column#1, plus(Column#1, 1))",
		},
		{
			solver: []PropagateConstantSolver{newPropConstSolver()},
			conditions: []Expression{
				newFunctionWithMockCtx(ast.EQ, newColumn(0), newColumn(1)),
				newFunctionWithMockCtx(ast.LE, newColumn(0), newFunctionWithMockCtx(ast.Rand)),
			},
			result: "eq(Column#0, Column#1), le(cast(Column#0, double BINARY), rand())",
		},
	}
	for _, tt := range tests {
		for _, solver := range tt.solver {
			ctx := mock.NewContext()
			conds := make([]Expression, 0, len(tt.conditions))
			for _, cd := range tt.conditions {
				conds = append(conds, FoldConstant(ctx, cd))
			}
			newConds := solver.PropagateConstant(ctx, conds)
			var result []string
			for _, v := range newConds {
				result = append(result, v.StringWithCtx(ctx, errors.RedactLogDisable))
			}
			sort.Strings(result)
			require.Equalf(t, tt.result, strings.Join(result, ", "), "different for expr %s", tt.conditions)
		}
	}
}

func TestConstantFolding(t *testing.T) {
	tests := []struct {
		condition       func(ctx BuildContext) Expression
		result          string
		nullRejectCheck bool
	}{
		{
			condition: func(ctx BuildContext) Expression {
				return newFunction(ctx, ast.LT, newColumn(0), newFunction(ctx, ast.Plus, newLonglong(1), newLonglong(2)))
			},
			result: "lt(Column#0, 3)",
		},
		{
			condition: func(ctx BuildContext) Expression {
				return newFunction(ctx, ast.LT, newColumn(0), newFunction(ctx, ast.Greatest, newLonglong(1), newLonglong(2)))
			},
			result: "lt(Column#0, 2)",
		},
		{
			condition: func(ctx BuildContext) Expression {
				return newFunction(ctx, ast.EQ, newColumn(0), newFunction(ctx, ast.Rand))
			},
			result: "eq(cast(Column#0, double BINARY), rand())",
		},
		{
			condition: func(ctx BuildContext) Expression {
				return newFunction(ctx, ast.IsNull, newLonglong(1))
			},
			result: "0",
		},
		{
			condition: func(ctx BuildContext) Expression {
				return newFunction(ctx, ast.EQ, newColumn(0), newFunction(ctx, ast.UnaryNot, newFunctionWithMockCtx(ast.Plus, newLonglong(1), newLonglong(1))))
			},
			result: "eq(Column#0, 0)",
		},
		{
			condition: func(ctx BuildContext) Expression {
				return newFunction(ctx, ast.LT, newColumn(0), newFunction(ctx, ast.Plus, newColumn(1), newFunctionWithMockCtx(ast.Plus, newLonglong(2), newLonglong(1))))
			},
			result: "lt(Column#0, plus(Column#1, 3))",
		},
		{
			condition: func(ctx BuildContext) Expression {
				expr := newFunction(ctx, ast.ConcatWS, newColumn(0), NewNull())
				return expr
			},
			nullRejectCheck: true,
			result:          "concat_ws(cast(Column#0, var_string(20)), <nil>)",
		},
	}
	for _, tt := range tests {
		ctx := mock.NewContext().GetExprCtx()
		require.False(t, ctx.IsInNullRejectCheck())
		expr := tt.condition(ctx)
		if tt.nullRejectCheck {
			ctx = exprctx.WithNullRejectCheck(ctx)
			require.True(t, ctx.IsInNullRejectCheck())
		}
		newConds := FoldConstant(ctx, expr)
		require.Equalf(t, tt.result, newConds.StringWithCtx(ctx.GetEvalCtx(), errors.RedactLogDisable), "different for expr %s", tt.condition)
	}
}

func TestConstantFoldingCharsetConvert(t *testing.T) {
	ctx := mock.NewContext()
	tests := []struct {
		condition Expression
		result    string
	}{
		{
			condition: newFunction(ctx, ast.Length, newFunctionWithType(
				ctx,
				InternalFuncToBinary, types.NewFieldType(mysql.TypeVarchar),
				newString("中文", "gbk_bin"))),
			result: "4",
		},
		{
			condition: newFunction(ctx, ast.Length, newFunctionWithType(
				ctx,
				InternalFuncToBinary, types.NewFieldType(mysql.TypeVarchar),
				newString("中文", "utf8mb4_bin"))),
			result: "6",
		},
		{
			condition: newFunction(ctx, ast.Concat, newFunctionWithType(
				ctx,
				InternalFuncFromBinary, types.NewFieldType(mysql.TypeVarchar),
				newString("中文", "binary"))),
			result: "中文",
		},
		{
			condition: newFunction(ctx, ast.Concat,
				newFunctionWithType(
					ctx,
					InternalFuncFromBinary, types.NewFieldTypeWithCollation(mysql.TypeVarchar, "gbk_bin", -1),
					newString("\xd2\xbb", "binary")),
				newString("中文", "gbk_bin"),
			),
			result: "一中文",
		},
		{
			condition: newFunction(ctx, ast.Concat,
				newString("中文", "gbk_bin"),
				newFunctionWithType(
					ctx,
					InternalFuncFromBinary, types.NewFieldTypeWithCollation(mysql.TypeVarchar, "gbk_bin", -1),
					newString("\xd2\xbb", "binary")),
			),
			result: "中文一",
		},
		// The result is binary charset, so gbk constant will convert to binary which is \xd6\xd0\xce\xc4.
		{
			condition: newFunction(ctx, ast.Concat,
				newString("中文", "gbk_bin"),
				newString("\xd2\xbb", "binary"),
			),
			result: "\xd6\xd0\xce\xc4\xd2\xbb",
		},
	}
	for _, tt := range tests {
		newConds := FoldConstant(ctx, tt.condition)
		require.Equalf(t, tt.result, newConds.StringWithCtx(ctx, errors.RedactLogDisable), "different for expr %s", tt.condition)
	}
}

func TestDeferredParamNotNull(t *testing.T) {
	ctx := mock.NewContext()
	testTime := time.Now()
	ctx.GetSessionVars().PlanCacheParams.Append(
		types.NewIntDatum(1),
		types.NewDecimalDatum(types.NewDecFromStringForTest("20170118123950.123")),
		types.NewTimeDatum(types.NewTime(types.FromGoTime(testTime), mysql.TypeTimestamp, 6)),
		types.NewDurationDatum(types.ZeroDuration),
		types.NewStringDatum("{}"),
		types.NewBinaryLiteralDatum([]byte{1}),
		types.NewBytesDatum([]byte{'b'}),
		types.NewFloat32Datum(1.1),
		types.NewFloat64Datum(2.1),
		types.NewUintDatum(100),
		types.NewMysqlBitDatum([]byte{1}),
		types.NewMysqlEnumDatum(types.Enum{Name: "n", Value: 2}),
	)
	cstInt := &Constant{ParamMarker: &ParamMarker{order: 0}, RetType: newIntFieldType()}
	cstDec := &Constant{ParamMarker: &ParamMarker{order: 1}, RetType: newDecimalFieldType()}
	cstTime := &Constant{ParamMarker: &ParamMarker{order: 2}, RetType: newDateFieldType()}
	cstDuration := &Constant{ParamMarker: &ParamMarker{order: 3}, RetType: newDurFieldType()}
	cstJSON := &Constant{ParamMarker: &ParamMarker{order: 4}, RetType: newJSONFieldType()}
	cstBytes := &Constant{ParamMarker: &ParamMarker{order: 6}, RetType: newBlobFieldType()}
	cstBinary := &Constant{ParamMarker: &ParamMarker{order: 5}, RetType: newBinaryLiteralFieldType()}
	cstFloat32 := &Constant{ParamMarker: &ParamMarker{order: 7}, RetType: newFloatFieldType()}
	cstFloat64 := &Constant{ParamMarker: &ParamMarker{order: 8}, RetType: newFloatFieldType()}
	cstUint := &Constant{ParamMarker: &ParamMarker{order: 9}, RetType: newIntFieldType()}
	cstBit := &Constant{ParamMarker: &ParamMarker{order: 10}, RetType: newBinaryLiteralFieldType()}
	cstEnum := &Constant{ParamMarker: &ParamMarker{order: 11}, RetType: newEnumFieldType()}

	require.Equal(t, mysql.TypeVarString, cstJSON.GetType(ctx).GetType())
	require.Equal(t, mysql.TypeNewDecimal, cstDec.GetType(ctx).GetType())
	require.Equal(t, mysql.TypeLonglong, cstInt.GetType(ctx).GetType())
	require.Equal(t, mysql.TypeLonglong, cstUint.GetType(ctx).GetType())
	require.Equal(t, mysql.TypeTimestamp, cstTime.GetType(ctx).GetType())
	require.Equal(t, mysql.TypeDuration, cstDuration.GetType(ctx).GetType())
	require.Equal(t, mysql.TypeBlob, cstBytes.GetType(ctx).GetType())
	require.Equal(t, mysql.TypeVarString, cstBinary.GetType(ctx).GetType())
	require.Equal(t, mysql.TypeVarString, cstBit.GetType(ctx).GetType())
	require.Equal(t, mysql.TypeFloat, cstFloat32.GetType(ctx).GetType())
	require.Equal(t, mysql.TypeDouble, cstFloat64.GetType(ctx).GetType())
	require.Equal(t, mysql.TypeEnum, cstEnum.GetType(ctx).GetType())

	d, _, err := cstInt.EvalInt(ctx, chunk.Row{})
	require.NoError(t, err)
	require.Equal(t, int64(1), d)
	r, _, err := cstFloat64.EvalReal(ctx, chunk.Row{})
	require.NoError(t, err)
	require.Equal(t, float64(2.1), r)
	de, _, err := cstDec.EvalDecimal(ctx, chunk.Row{})
	require.NoError(t, err)
	require.Equal(t, "20170118123950.123", de.String())
	s, _, err := cstBytes.EvalString(ctx, chunk.Row{})
	require.NoError(t, err)
	require.Equal(t, "b", s)
	evalTime, _, err := cstTime.EvalTime(ctx, chunk.Row{})
	require.NoError(t, err)
	v := ctx.GetSessionVars().PlanCacheParams.GetParamValue(2)
	require.Equal(t, 0, evalTime.Compare(v.GetMysqlTime()))
	dur, _, err := cstDuration.EvalDuration(ctx, chunk.Row{})
	require.NoError(t, err)
	require.Equal(t, types.ZeroDuration.Duration, dur.Duration)
	evalJSON, _, err := cstJSON.EvalJSON(ctx, chunk.Row{})
	require.NoError(t, err)
	require.NotNil(t, evalJSON)
}

func TestDeferredExprNotNull(t *testing.T) {
	m := &MockExpr{}
	ctx := mock.NewContext()
	cst := &Constant{DeferredExpr: m, RetType: newIntFieldType()}
	m.i, m.err = nil, fmt.Errorf("ERROR")
	_, _, err := cst.EvalInt(ctx, chunk.Row{})
	require.Error(t, err)
	_, _, err = cst.EvalReal(ctx, chunk.Row{})
	require.Error(t, err)
	_, _, err = cst.EvalDecimal(ctx, chunk.Row{})
	require.Error(t, err)
	_, _, err = cst.EvalString(ctx, chunk.Row{})
	require.Error(t, err)
	_, _, err = cst.EvalTime(ctx, chunk.Row{})
	require.Error(t, err)
	_, _, err = cst.EvalDuration(ctx, chunk.Row{})
	require.Error(t, err)
	_, _, err = cst.EvalJSON(ctx, chunk.Row{})
	require.Error(t, err)

	m.i, m.err = nil, nil
	_, isNull, err := cst.EvalInt(ctx, chunk.Row{})
	require.NoError(t, err)
	require.True(t, isNull)
	_, isNull, err = cst.EvalReal(ctx, chunk.Row{})
	require.NoError(t, err)
	require.True(t, isNull)
	_, isNull, err = cst.EvalDecimal(ctx, chunk.Row{})
	require.NoError(t, err)
	require.True(t, isNull)
	_, isNull, err = cst.EvalString(ctx, chunk.Row{})
	require.NoError(t, err)
	require.True(t, isNull)
	_, isNull, err = cst.EvalTime(ctx, chunk.Row{})
	require.NoError(t, err)
	require.True(t, isNull)
	_, isNull, err = cst.EvalDuration(ctx, chunk.Row{})
	require.NoError(t, err)
	require.True(t, isNull)
	_, isNull, err = cst.EvalJSON(ctx, chunk.Row{})
	require.NoError(t, err)
	require.True(t, isNull)

	m.i = int64(2333)
	xInt, _, _ := cst.EvalInt(ctx, chunk.Row{})
	require.Equal(t, int64(2333), xInt)

	m.i = float64(123.45)
	xFlo, _, _ := cst.EvalReal(ctx, chunk.Row{})
	require.Equal(t, float64(123.45), xFlo)

	m.i = "abc"
	xStr, _, _ := cst.EvalString(ctx, chunk.Row{})
	require.Equal(t, "abc", xStr)

	m.i = &types.MyDecimal{}
	xDec, _, _ := cst.EvalDecimal(ctx, chunk.Row{})
	require.Equal(t, 0, xDec.Compare(m.i.(*types.MyDecimal)))

	m.i = types.ZeroTime
	xTim, _, _ := cst.EvalTime(ctx, chunk.Row{})
	require.Equal(t, 0, xTim.Compare(m.i.(types.Time)))

	m.i = types.Duration{}
	xDur, _, _ := cst.EvalDuration(ctx, chunk.Row{})
	require.Equal(t, 0, xDur.Compare(m.i.(types.Duration)))

	m.i = types.BinaryJSON{}
	xJsn, _, _ := cst.EvalJSON(ctx, chunk.Row{})
	require.Equal(t, xJsn.String(), m.i.(types.BinaryJSON).String())

	cln := cst.Clone().(*Constant)
	require.Equal(t, cst.DeferredExpr, cln.DeferredExpr)
}

func TestVectorizedConstant(t *testing.T) {
	// fixed-length type with/without Sel
	for _, cst := range []*Constant{
		{RetType: newIntFieldType(), Value: types.NewIntDatum(2333)},
		{RetType: newIntFieldType(), DeferredExpr: &Constant{RetType: newIntFieldType(), Value: types.NewIntDatum(2333)}}} {
		chk := chunk.New([]*types.FieldType{newIntFieldType()}, 1024, 1024)
		for i := range 1024 {
			chk.AppendInt64(0, int64(i))
		}
		col := chunk.NewColumn(newIntFieldType(), 1024)
		ctx := mock.NewContext()
		require.Nil(t, cst.VecEvalInt(ctx, chk, col))
		i64s := col.Int64s()
		require.Equal(t, 1024, len(i64s))
		for _, v := range i64s {
			require.Equal(t, int64(2333), v)
		}

		// fixed-length type with Sel
		sel := []int{2, 3, 5, 7, 11, 13, 17, 19, 23, 29}
		chk.SetSel(sel)
		require.Nil(t, cst.VecEvalInt(ctx, chk, col))
		i64s = col.Int64s()
		for i := range sel {
			require.Equal(t, int64(2333), i64s[i])
		}
	}

	// var-length type with/without Sel
	for _, cst := range []*Constant{
		{RetType: newStringFieldType(), Value: types.NewStringDatum("hello")},
		{RetType: newStringFieldType(), DeferredExpr: &Constant{RetType: newStringFieldType(), Value: types.NewStringDatum("hello")}}} {
		chk := chunk.New([]*types.FieldType{newIntFieldType()}, 1024, 1024)
		for i := range 1024 {
			chk.AppendInt64(0, int64(i))
		}
		cst = &Constant{DeferredExpr: nil, RetType: newStringFieldType(), Value: types.NewStringDatum("hello")}
		chk.SetSel(nil)
		col := chunk.NewColumn(newStringFieldType(), 1024)
		ctx := mock.NewContext()
		require.Nil(t, cst.VecEvalString(ctx, chk, col))
		for i := range 1024 {
			require.Equal(t, "hello", col.GetString(i))
		}

		// var-length type with Sel
		sel := []int{2, 3, 5, 7, 11, 13, 17, 19, 23, 29}
		chk.SetSel(sel)
		require.Nil(t, cst.VecEvalString(ctx, chk, col))
		for i := range sel {
			require.Equal(t, "hello", col.GetString(i))
		}
	}
}

func TestGetTypeThreadSafe(t *testing.T) {
	ctx := mock.NewContext()
	ctx.GetSessionVars().PlanCacheParams.Append(types.NewIntDatum(1))
	con := &Constant{ParamMarker: &ParamMarker{order: 0}, RetType: newStringFieldType()}
	ft1 := con.GetType(ctx)
	ft2 := con.GetType(ctx)
	require.NotSame(t, ft1, ft2)
}

func TestSpecificConstant(t *testing.T) {
	one := NewOne()
	require.Equal(t, one.Value, types.NewDatum(1))
	require.Equal(t, one.RetType.GetType(), mysql.TypeTiny)
	require.Equal(t, one.RetType.GetFlen(), 1)
	require.Equal(t, one.RetType.GetDecimal(), 0)

	zero := NewZero()
	require.Equal(t, zero.Value, types.NewDatum(0))
	require.Equal(t, zero.RetType.GetType(), mysql.TypeTiny)
	require.Equal(t, zero.RetType.GetFlen(), 1)
	require.Equal(t, zero.RetType.GetDecimal(), 0)

	null := NewNull()
	require.Equal(t, null.Value, types.NewDatum(nil))
	require.Equal(t, null.RetType.GetType(), mysql.TypeTiny)
	require.Equal(t, null.RetType.GetFlen(), 1)
	require.Equal(t, null.RetType.GetDecimal(), 0)
}

func TestConstantHashEquals(t *testing.T) {
	// Test for Hash64 interface
	cst1 := &Constant{Value: types.NewIntDatum(2333), RetType: newIntFieldType()}
	cst2 := &Constant{Value: types.NewIntDatum(2333), RetType: newIntFieldType()}
	hasher1 := base.NewHashEqualer()
	hasher2 := base.NewHashEqualer()
	cst1.Hash64(hasher1)
	cst2.Hash64(hasher2)
	require.Equal(t, hasher1.Sum64(), hasher2.Sum64())
	require.True(t, cst1.Equals(cst2))

	// test cst2 datum changes.
	cst2.Value = types.NewIntDatum(2334)
	hasher2.Reset()
	cst2.Hash64(hasher2)
	require.NotEqual(t, hasher1.Sum64(), hasher2.Sum64())
	require.False(t, cst1.Equals(cst2))

	// test cst2 type changes.
	cst2.Value = types.NewIntDatum(2333)
	cst2.RetType = newStringFieldType()
	hasher2.Reset()
	cst2.Hash64(hasher2)
	require.NotEqual(t, hasher1.Sum64(), hasher2.Sum64())
	require.False(t, cst1.Equals(cst2))
}
