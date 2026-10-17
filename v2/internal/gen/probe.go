package gen

import (
	"context"

	"gonghao/internal/gateway"
)

// ResearchProbe runs only the research step for a user's tier, for manual verification against
// real models. It is not wired to any route.
func (p *Pipeline) ResearchProbe(ctx context.Context, userID int64, tier, topic, query string) ([]Evidence, []Step) {
	var steps []Step
	trace := func(s Step) { steps = append(steps, s) }
	load := func(stage string) ([]gateway.Config, error) { return gateway.LoadStage(ctx, p.DB, p.Box, stage, tier) }
	chat := func(stage string, cfgs []gateway.Config, sys, user string) (gateway.Result, error) {
		r, err := p.GW.Chat(ctx, cfgs, gateway.Request{Stage: stage, Messages: []gateway.Message{{Role: "system", Content: sys}, {Role: "user", Content: user}}}, 0)
		trace(Step{Stage: stage, Attempts: r.Attempts})
		return r, err
	}
	ev := p.research(ctx, Job{UserID: userID}, topic, query, load, chat, trace)
	return ev, steps
}

// ScanProbe sends one pre-scan request with the given user message, for comparing query variants.
func (p *Pipeline) ScanProbe(ctx context.Context, tier, user string) ([]Evidence, gateway.Result, error) {
	cfgs, err := gateway.LoadStage(ctx, p.DB, p.Box, "research", tier)
	if err != nil {
		return nil, gateway.Result{}, err
	}
	r, err := p.GW.Chat(ctx, cfgs, gateway.Request{Stage: "scan", Messages: []gateway.Message{{Role: "system", Content: scanSystem}, {Role: "user", Content: user}}}, 0)
	return ParseFacts(r.Text), r, err
}
