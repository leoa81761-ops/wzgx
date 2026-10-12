package pipeline

import "testing"

func TestAccountVoiceReachesThePrompt(t *testing.T) {
	in := (&work{
		Audience: "物业工程", Keywords: "验收,机房", BannedWords: "1000V", TopicMode: "rotate", ImageMode: "none",
	}).input()
	fields := voiceFields(in)
	if fields["audience"] != "物业工程" || fields["keywords"] != "验收,机房" || fields["banned_words"] != "1000V" || fields["topic_mode"] != "rotate" {
		t.Fatal(fields)
	}
	if hotAllowed(&in) {
		t.Fatal("rotate should skip hot topics")
	}
	auto := in
	auto.TopicMode = "auto"
	auto.Tier = "premium"
	if !hotAllowed(&auto) {
		t.Fatal("auto still allows hot topics")
	}
	words := mergeBanned([]string{"诊断"}, "收益，内部价,诊断")
	if len(words) != 3 || words[1] != "收益" || words[2] != "内部价" {
		t.Fatal(words)
	}
}

func TestImagePlanModes(t *testing.T) {
	use, cover, inline := ImagePlan("auto", false, true, 2)
	if !use || !cover || inline != 2 {
		t.Fatal(use, cover, inline)
	}
	use, cover, inline = ImagePlan("none", true, true, 2)
	if use || cover || inline != 0 {
		t.Fatal(use, cover, inline)
	}
	use, cover, inline = ImagePlan("cover", false, true, 2)
	if !use || !cover || inline != 0 {
		t.Fatal(use, cover, inline)
	}
	use, _, inline = ImagePlan("asset", true, true, 2)
	if use || inline != 0 {
		t.Fatal(use, inline)
	}
	use, cover, inline = ImagePlan("asset", false, true, 2)
	if !use || !cover || inline != 2 {
		t.Fatal(use, cover, inline)
	}
}
