// GORM clause 辅助：ON CONFLICT DO UPDATE（PG upsert）
package dal

import (
	"gorm.io/gorm/clause"
)

// OnConflictUpsert 返回 ON CONFLICT (...) DO UPDATE SET col=EXCLUDED.col 子句
// conflictCols 是 ON CONFLICT 后面的列（与表 UNIQUE 索引对齐）
// updateCols 是 SET 子句中的列（值 = EXCLUDED.<col>）
func OnConflictUpsert(conflictCols, updateCols []string) clause.OnConflict {
	return clause.OnConflict{
		Columns:   toClauseCols(conflictCols),
		DoUpdates: clause.Assignments(toUpdateMap(updateCols)),
	}
}

func toClauseCols(cols []string) []clause.Column {
	out := make([]clause.Column, 0, len(cols))
	for _, c := range cols {
		out = append(out, clause.Column{Name: c})
	}
	return out
}

func toUpdateMap(cols []string) map[string]interface{} {
	out := make(map[string]interface{}, len(cols))
	for _, c := range cols {
		out[c] = clause.Expr{SQL: "EXCLUDED." + c}
	}
	return out
}
