// 文件: src/internal/rank/rank_test.go
package rank

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"llm-hub/src/internal/models"
)

func TestAllBoardsContainOnlyRealModels(t *testing.T) {
	rep, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if rep.TotalModels != 460 {
		t.Fatalf("TotalModels=%d want 460 (真实数据集)", rep.TotalModels)
	}
	if rep.TotalProviders != 69 {
		t.Fatalf("TotalProviders=%d want 69", rep.TotalProviders)
	}
	if len(rep.Boards) != len(Dimensions) {
		t.Fatalf("boards=%d want %d", len(rep.Boards), len(Dimensions))
	}
	idset, err := models.IDSet()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for dim, b := range rep.Boards {
		if b.SourceSHA256 == "" || b.SourceSHA256 != models.SourceSHA256() {
			t.Fatalf("dimension %s: source_sha256 缺失或与真实字节不一致", dim)
		}
		if b.EntryCount != len(b.Entries) {
			t.Fatalf("dimension %s: entry_count=%d != len(entries)=%d", dim, b.EntryCount, len(b.Entries))
		}
		for i, e := range b.Entries {
			if e.Rank != i+1 {
				t.Fatalf("dimension %s: rank 不连续 at %d", dim, i)
			}
			if e.Source != "data/models-real.json" {
				t.Fatalf("dimension %s: entry %s 缺少真实来源标注", dim, e.ID)
			}
			if dim == "providers" {
				continue
			}
			checked++
			if !idset[e.ID] {
				t.Fatalf("dimension %s: 编造模型 id %q 不在 data/models-real.json", dim, e.ID)
			}
		}
	}
	t.Logf("checked=%d real entries across %d dimensions, no fabricated id", checked, len(rep.Boards))
}

func TestSourceSHA256MatchesRealFileOnDisk(t *testing.T) {
	p, err := models.FindRealModelsFile()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	want := hex.EncodeToString(sum[:])
	if got := models.SourceSHA256(); got != want {
		t.Fatalf("embed SHA=%s 与磁盘真实文件 SHA=%s 不一致", got, want)
	}
	if got := want; got != "08b4635fd684689feda841f906d59e0dcd8ad9dd57a7c0b7f0667d46b740876a" {
		t.Fatalf("真实数据集 SHA-256 已变化: %s", got)
	}
}

func TestSortingIsReproducible(t *testing.T) {
	ctx, err := Compute("context")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(ctx.Entries); i++ {
		if ctx.Entries[i-1].Value < ctx.Entries[i].Value {
			t.Fatalf("context 榜未按降序: %f < %f", ctx.Entries[i-1].Value, ctx.Entries[i].Value)
		}
	}
	asc, err := Compute("price_asc")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(asc.Entries); i++ {
		if asc.Entries[i-1].Value > asc.Entries[i].Value {
			t.Fatalf("price_asc 榜未按升序: %f > %f", asc.Entries[i-1].Value, asc.Entries[i].Value)
		}
	}
	if asc.EntryCount >= ctx.EntryCount {
		t.Fatalf("最低付费榜应排除免费/缺失价格条目: asc=%d ctx=%d", asc.EntryCount, ctx.EntryCount)
	}
}

func TestVerifyReportsNoFabrication(t *testing.T) {
	v, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if v["verified"] != true {
		t.Fatalf("Verify 未通过: %+v", v)
	}
	if v["fabricated_entries"].(int) != 0 {
		t.Fatalf("存在编造条目: %+v", v)
	}
	if v["total_models"].(int) != 460 {
		t.Fatalf("total_models=%v", v["total_models"])
	}
	t.Logf("verify=%+v", v)
}

func TestUnknownDimensionRejected(t *testing.T) {
	if _, err := Compute("fake_score_board"); err == nil {
		t.Fatal("未知维度必须拒绝，防止引入编造榜单")
	}
}
