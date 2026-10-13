package persona

import (
	"strings"
	"testing"
)

func TestGrandmaVoiceComesFromTheAccountName(t *testing.T) {
	home := SuggestNamed("奶奶不当家", "家庭生活", "family")
	site := SuggestNamed("弱电陈工", "弱电智能化", "elv")
	if !strings.Contains(home.Tone, "奶奶") || !strings.Contains(home.Signatures, "我在家见过") {
		t.Fatal(home)
	}
	if strings.Contains(site.Tone, "奶奶") || strings.Contains(site.Signatures, "我在家见过") {
		t.Fatal(site)
	}
	kept := Fill(Profile{Tone: "我改过的语气"}, home)
	if kept.Tone != "我改过的语气" || kept.Signatures != home.Signatures {
		t.Fatal(kept)
	}
}

func TestSuggestDiffersByDirection(t *testing.T) {
	elv := Suggest("弱电智能化", "elv")
	home := Suggest("母婴", "family")
	work := Suggest("职场成长", "")
	if elv.Tone == home.Tone || elv.Signatures == work.Signatures {
		t.Fatal(elv, home, work)
	}
	if elv.Audience != "做工程和物业的读者" || elv.Columns == "" || elv.Taboos == "" {
		t.Fatal(elv)
	}
	kept := Fill(Profile{Tone: "我的语气", Audience: "项目经理"}, elv)
	if kept.Tone != "我的语气" || kept.Audience != "项目经理" || kept.Signatures != elv.Signatures {
		t.Fatal(kept)
	}
}

func TestPersonaInheritsFromAccount(t *testing.T) {
	suggested := Suggest("弱电智能化", "elv")
	account := Profile{Tone: "账号语气", Audience: "现场负责人", Columns: "验收清单", Signatures: "先看现场", Taboos: "包过"}
	inherited := Effective(Profile{}, account, suggested)
	if inherited.Tone != "账号语气" || inherited.Audience != "现场负责人" || inherited.Columns != "验收清单" || inherited.Taboos != "包过" {
		t.Fatal(inherited)
	}
	overridden := Effective(Profile{Tone: "计划语气"}, account, suggested)
	if overridden.Tone != "计划语气" || overridden.Audience != "现场负责人" || overridden.Signatures != "先看现场" {
		t.Fatal(overridden)
	}
	blank := Effective(Profile{}, Profile{}, suggested)
	if blank.Tone != suggested.Tone || blank.Audience != suggested.Audience || blank.Taboos != suggested.Taboos {
		t.Fatal(blank)
	}
}
