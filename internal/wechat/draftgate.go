package wechat

import (
	"context"
	"fmt"
)

// MassSendNote 写进手动发布提醒。本产品不调用群发接口。
const MassSendNote = "本产品不调用群发。订阅号每天可群发 1 次，服务号每月可群发 4 次。请在公众号助手里自行发布。"

// DraftWait 在新建草稿前看开关和总数。探测失败时不拦截，交给后面的写入自己报错。
// cap 小于等于 0 时不按数量拦截。官方总数接口没有公布硬上限，数量上限来自设置项。
func DraftWait(ctx context.Context, client Client, cap int) (string, bool) {
	open, _, err := client.DraftSwitch(ctx)
	if err != nil {
		return "", false
	}
	if !open {
		return "草稿箱等待：草稿箱开关处于关闭。请在公众平台打开后再试。" + MassSendNote, true
	}
	if cap <= 0 {
		return "", false
	}
	n, err := client.DraftCount(ctx)
	if err != nil {
		return "", false
	}
	if n >= cap {
		return fmt.Sprintf("草稿箱等待：草稿箱已有 %d 篇，达到上限 %d。请先清理后再试。%s", n, cap, MassSendNote), true
	}
	return "", false
}
