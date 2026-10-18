// Command migrate-v1 copies users, Official Accounts, writing settings, columns, plans, model configs,
// stage routes and Tavily keys from the v1 database into v2.
//
//   - v1 is opened read-only (every transaction is READ ONLY); nothing is written there.
//   - Rerunnable: each imported row is recorded in v2's v1_import table and skipped next time,
//     so later edits in v2 are never overwritten.
//   - Password hashes are kept as-is (v2 uses the same PBKDF2 format). Secrets and API keys are
//     decrypted with V1_MASTER_KEY and re-sealed with V2_MASTER_KEY in memory; they are never printed.
//   - Plans arrive disabled, so nothing runs by itself at cutover.
//   - Output is counts plus skipped rows by v1 id and reason; no names, emails or secrets.
//
// Env: V1_DATABASE_URL, V1_MASTER_KEY (or V1_MASTER_KEY_FILE), V2_DATABASE_URL, V2_MASTER_KEY.
// Flag -dry-run rolls the v2 transaction back after reporting.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/crypto"
	"gonghao/internal/db"
)

type report struct {
	imported map[string]int
	already  map[string]int
	skipped  []string
}

func (r *report) skip(kind string, id int64, why string) {
	r.skipped = append(r.skipped, fmt.Sprintf("%s v1#%d: %s", kind, id, why))
}

type importer struct {
	v1     *pgxpool.Pool
	tx     pgx.Tx
	box    *crypto.Box
	master string
	rep    *report
}

func main() {
	dry := flag.Bool("dry-run", false, "report what would be imported, then roll back")
	flag.BoolVar(&backupConfigs, "backup-configs", false, "also copy v1's ai.1so.org model configs again as separate backup configs (named \"… · ai.1so 备用\"); routes are not changed")
	flag.Parse()
	if err := run(context.Background(), *dry); err != nil {
		fmt.Fprintln(os.Stderr, "migrate-v1:", err)
		os.Exit(1)
	}
}

var backupConfigs bool

func env(k string) string { return strings.TrimSpace(os.Getenv(k)) }

func run(ctx context.Context, dry bool) error {
	master := env("V1_MASTER_KEY")
	if f := env("V1_MASTER_KEY_FILE"); master == "" && f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read V1_MASTER_KEY_FILE: %w", err)
		}
		master = strings.TrimSpace(string(b))
	}
	if env("V1_DATABASE_URL") == "" || env("V2_DATABASE_URL") == "" || master == "" || env("V2_MASTER_KEY") == "" {
		return errors.New("need V1_DATABASE_URL, V1_MASTER_KEY(_FILE), V2_DATABASE_URL, V2_MASTER_KEY")
	}
	box, err := crypto.NewBox(env("V2_MASTER_KEY"))
	if err != nil {
		return err
	}
	cfg, err := pgxpool.ParseConfig(env("V1_DATABASE_URL"))
	if err != nil {
		return err
	}
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	v1, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer v1.Close()
	v2, err := db.Open(ctx, env("V2_DATABASE_URL"))
	if err != nil {
		return err
	}
	defer v2.Close()
	if _, err := db.Migrate(ctx, v2); err != nil {
		return err
	}
	tx, err := v2.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	im := &importer{v1: v1, tx: tx, box: box, master: master, rep: &report{imported: map[string]int{}, already: map[string]int{}}}
	steps := []struct {
		name string
		f    func(context.Context) error
	}{
		{"users", im.users}, {"tavily", im.tavily}, {"configs", im.configs}, {"backup configs", im.backupConfigs}, {"routes", im.routes},
		{"accounts", im.accounts}, {"columns", im.columns}, {"plans", im.plans},
	}
	for _, s := range steps {
		if err := s.f(ctx); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	im.print(dry)
	if dry {
		return nil
	}
	return tx.Commit(ctx)
}

func (im *importer) print(dry bool) {
	kinds := map[string]bool{}
	for k := range im.rep.imported {
		kinds[k] = true
	}
	for k := range im.rep.already {
		kinds[k] = true
	}
	var ks []string
	for k := range kinds {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	if dry {
		fmt.Println("DRY RUN — nothing committed")
	}
	for _, k := range ks {
		fmt.Printf("%-9s imported %d, already there %d\n", k, im.rep.imported[k], im.rep.already[k])
	}
	fmt.Printf("skipped   %d\n", len(im.rep.skipped))
	for _, s := range im.rep.skipped {
		fmt.Println("  - " + s)
	}
}

// mapped returns the v2 id for an already-imported v1 row.
func (im *importer) mapped(ctx context.Context, kind string, old int64) (int64, bool, error) {
	var id int64
	err := im.tx.QueryRow(ctx, `SELECT new_id FROM v1_import WHERE kind=$1 AND old_id=$2`, kind, old).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

func (im *importer) record(ctx context.Context, kind string, old, new int64) error {
	_, err := im.tx.Exec(ctx, `INSERT INTO v1_import (kind, old_id, new_id) VALUES ($1,$2,$3)`, kind, old, new)
	if err == nil {
		im.rep.imported[kind]++
	}
	return err
}

func (im *importer) reseal(token string) (sealed, mask string, err error) {
	plain, err := v1Open(im.master, token)
	if err != nil {
		return "", "", err
	}
	if sealed, err = im.box.Seal(plain); err != nil {
		return "", "", err
	}
	return sealed, crypto.Mask(plain), nil
}

// hashShapeOK: v1 and v2 both store base64(salt)$base64(pbkdf2-sha256 key), so hashes carry over as-is.
func hashShapeOK(h string) bool {
	s, k, ok := strings.Cut(h, "$")
	if !ok {
		return false
	}
	salt, e1 := base64.StdEncoding.DecodeString(s)
	key, e2 := base64.StdEncoding.DecodeString(k)
	return e1 == nil && e2 == nil && len(salt) > 0 && len(key) == 32
}

func parseTime(s string) time.Time {
	for _, f := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999-07:00", "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05", "2006-01-02T15:04:05.999999"} {
		if t, err := time.Parse(f, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Now()
}

var tierMap = map[string]string{"free": "free", "standard": "standard", "premium": "premium", "lifetime": "buyout", "buyout": "buyout", "custom": "custom"}

func (im *importer) users(ctx context.Context) error {
	rows, err := im.v1.Query(ctx, `SELECT u.id, COALESCE(u.email,''), COALESCE(u.phone,''), COALESCE(u.password_hash,''), COALESCE(u.tier,'free'),
		COALESCE(u.is_admin,0), COALESCE(u.is_active,1), COALESCE(u.quota_exempt,0), COALESCE(u.created_at,''),
		(SELECT count(*) FROM wx_account a WHERE a.user_id=u.id AND a.bind_status='active')
		FROM users u ORDER BY u.id`)
	if err != nil {
		return err
	}
	type u struct {
		id                       int64
		email, phone, hash, tier string
		admin, active, exempt    int
		created                  string
		bound                    int
	}
	var list []u
	for rows.Next() {
		var x u
		if err := rows.Scan(&x.id, &x.email, &x.phone, &x.hash, &x.tier, &x.admin, &x.active, &x.exempt, &x.created, &x.bound); err != nil {
			rows.Close()
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		if _, ok, err := im.mapped(ctx, "user", x.id); err != nil {
			return err
		} else if ok {
			im.rep.already["user"]++
			continue
		}
		email := strings.ToLower(strings.TrimSpace(x.email))
		if email == "" || !strings.Contains(email, "@") {
			im.rep.skip("user", x.id, "no email (v2 signs in by email)")
			continue
		}
		if !hashShapeOK(x.hash) {
			im.rep.skip("user", x.id, "password hash format not recognised")
			continue
		}
		// an account with this email already in v2 (e.g. the bootstrap admin): link, don't duplicate
		var existing int64
		err := im.tx.QueryRow(ctx, `SELECT id FROM users WHERE lower(email)=$1`, email).Scan(&existing)
		if err == nil {
			if err := im.record(ctx, "user", x.id, existing); err != nil {
				return err
			}
			im.rep.imported["user"]--
			im.rep.already["user"]++
			im.rep.skip("user", x.id, "email already in v2; linked to the existing user, nothing changed")
			continue
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		role, tier := "user", tierMap[x.tier]
		if x.admin == 1 {
			role = "super_admin"
		}
		if tier == "" {
			tier = "custom"
			im.rep.skip("user", x.id, "unknown tier "+x.tier+" imported as custom (user kept)")
		}
		status := "active"
		if x.active == 0 {
			status = "disabled"
		}
		maxAcc := max(1, x.bound)
		if role != "user" {
			maxAcc = 0
		}
		var phone *string
		if p := strings.TrimSpace(x.phone); p != "" {
			var taken int
			im.tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE phone=$1`, p).Scan(&taken)
			if taken == 0 {
				phone = &p
			}
		}
		var id int64
		if err := im.tx.QueryRow(ctx, `INSERT INTO users (email, phone, password_hash, role, tier, max_accounts, quota_exempt, status, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, email, phone, x.hash, role, tier, maxAcc, x.exempt == 1, status, parseTime(x.created)).Scan(&id); err != nil {
			return err
		}
		if err := im.record(ctx, "user", x.id, id); err != nil {
			return err
		}
	}
	return nil
}

func (im *importer) tavily(ctx context.Context) error {
	rows, err := im.v1.Query(ctx, `SELECT user_id, COALESCE(tavily_key_encrypted,'') FROM user_secret ORDER BY user_id`)
	if err != nil {
		return err
	}
	type s struct {
		uid   int64
		token string
	}
	var list []s
	for rows.Next() {
		var x s
		if err := rows.Scan(&x.uid, &x.token); err != nil {
			rows.Close()
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		if x.token == "" {
			continue
		}
		if _, ok, err := im.mapped(ctx, "tavily", x.uid); err != nil {
			return err
		} else if ok {
			im.rep.already["tavily"]++
			continue
		}
		uid, ok, err := im.mapped(ctx, "user", x.uid)
		if err != nil {
			return err
		}
		if !ok {
			im.rep.skip("tavily", x.uid, "user not imported")
			continue
		}
		sealed, mask, err := im.reseal(x.token)
		if err != nil {
			im.rep.skip("tavily", x.uid, "cannot decrypt: "+err.Error())
			continue
		}
		if _, err := im.tx.Exec(ctx, `INSERT INTO user_secret (user_id, kind, cipher, mask) VALUES ($1,'tavily',$2,$3)
			ON CONFLICT (user_id, kind) DO NOTHING`, uid, sealed, mask); err != nil {
			return err
		}
		if err := im.record(ctx, "tavily", x.uid, uid); err != nil {
			return err
		}
	}
	return nil
}

func (im *importer) configs(ctx context.Context) error { return im.copyConfigs(ctx, "config") }

// backupConfigs: with -backup-configs, a second copy of each v1 ai.1so.org config (kind config_backup),
// so v2 can keep the old relay as a fallback after its main configs moved elsewhere.
func (im *importer) backupConfigs(ctx context.Context) error {
	if !backupConfigs {
		return nil
	}
	return im.copyConfigs(ctx, "config_backup")
}

func (im *importer) copyConfigs(ctx context.Context, kindTag string) error {
	rows, err := im.v1.Query(ctx, `SELECT id, COALESCE(config_type,''), COALESCE(name,''), COALESCE(protocol,'openai'), COALESCE(base_url,''),
		COALESCE(api_key_encrypted,''), COALESCE(model_name,''), COALESCE(extra_params,''), COALESCE(priority,100), COALESCE(enabled,1),
		COALESCE(first_byte_timeout_seconds,0), COALESCE(idle_timeout_seconds,0), COALESCE(total_timeout_seconds,0) FROM service_config ORDER BY id`)
	if err != nil {
		return err
	}
	type c struct {
		id                                          int64
		typ, name, proto, base, token, model, extra string
		prio, enabled, fb, idle, total              int
	}
	var list []c
	for rows.Next() {
		var x c
		if err := rows.Scan(&x.id, &x.typ, &x.name, &x.proto, &x.base, &x.token, &x.model, &x.extra, &x.prio, &x.enabled, &x.fb, &x.idle, &x.total); err != nil {
			rows.Close()
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	kinds := map[string]string{"text_model": "text", "image_model": "image"}
	for _, x := range list {
		kind := kinds[x.typ]
		if kind == "" {
			if kindTag == "config" {
				im.rep.skip("config", x.id, "type "+x.typ+" has no v2 equivalent (WeChat and integrations are configured by env in v2)")
			}
			continue
		}
		name := x.name
		if kindTag == "config_backup" {
			if !strings.Contains(x.base, "ai.1so.org") {
				continue
			}
			name = strings.TrimSpace(x.name) + " · ai.1so 备用"
		}
		if _, ok, err := im.mapped(ctx, kindTag, x.id); err != nil {
			return err
		} else if ok {
			im.rep.already[kindTag]++
			continue
		}
		params := map[string]any{}
		if strings.TrimSpace(x.extra) != "" {
			if json.Unmarshal([]byte(x.extra), &params) != nil {
				im.rep.skip("config", x.id, "extra params not JSON; imported without them")
				params = map[string]any{}
			}
		}
		effort, _ := params["reasoning_effort"].(string)
		delete(params, "reasoning_effort")
		pj, _ := json.Marshal(params)
		sealed, mask := "", ""
		if x.token != "" {
			var err error
			if sealed, mask, err = im.reseal(x.token); err != nil {
				im.rep.skip(kindTag, x.id, "cannot decrypt key: "+err.Error())
				continue
			}
		}
		pos := func(v, d int) int {
			if v > 0 {
				return v
			}
			return d
		}
		proto := x.proto
		if proto != "anthropic" {
			proto = "openai"
		}
		var id int64
		if err := im.tx.QueryRow(ctx, `INSERT INTO service_config (kind, name, protocol, base_url, key_cipher, key_mask, model, reasoning_effort, params,
			first_byte_timeout_s, idle_timeout_s, total_timeout_s, priority, enabled) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`,
			kind, name, proto, x.base, sealed, mask, x.model, effort, pj, pos(x.fb, 30), pos(x.idle, 180), pos(x.total, 900), x.prio, x.enabled == 1).Scan(&id); err != nil {
			return err
		}
		if err := im.record(ctx, kindTag, x.id, id); err != nil {
			return err
		}
	}
	return nil
}

func (im *importer) routes(ctx context.Context) error {
	stageMap := map[string]string{"topic": "topic", "research": "research", "draft": "write", "write": "write", "review": "review",
		"image": "image", "cover": "image", "illustration": "image"}
	rows, err := im.v1.Query(ctx, `SELECT stage, COALESCE(tier,'*'), service_config_id FROM stage_route ORDER BY stage, tier, sort_order, id`)
	if err != nil {
		return err
	}
	type key struct{ stage, tier string }
	order := []key{}
	ids := map[key][]int64{}
	dropped := map[string]bool{}
	for rows.Next() {
		var st, tier string
		var cid int64
		if err := rows.Scan(&st, &tier, &cid); err != nil {
			rows.Close()
			return err
		}
		s := stageMap[st]
		if s == "" {
			dropped[st] = true
			continue
		}
		k := key{s, tier}
		if _, seen := ids[k]; !seen {
			order = append(order, k)
		}
		ids[k] = append(ids[k], cid)
	}
	rows.Close()
	for st := range dropped {
		im.rep.skipped = append(im.rep.skipped, "route stage "+st+": no v2 equivalent (v2 writes in one stage)")
	}
	for _, k := range order {
		var n int
		im.tx.QueryRow(ctx, `SELECT count(*) FROM stage_route WHERE stage=$1 AND tier=$2`, k.stage, k.tier).Scan(&n)
		if n > 0 {
			im.rep.already["route"]++
			continue
		}
		var list []int64
		seen := map[int64]bool{}
		for _, old := range ids[k] {
			id, ok, err := im.mapped(ctx, "config", old)
			if err != nil {
				return err
			}
			if ok && !seen[id] {
				seen[id] = true
				list = append(list, id)
			}
		}
		if len(list) == 0 {
			im.rep.skipped = append(im.rep.skipped, "route stage "+k.stage+": none of its configs were imported")
			continue
		}
		if _, err := im.tx.Exec(ctx, `INSERT INTO stage_route (stage, tier, config_ids) VALUES ($1,$2,$3)`, k.stage, k.tier, list); err != nil {
			return err
		}
		im.rep.imported["route"]++
	}
	// v1 image configs (e.g. old #8 grok-imagine) serve v2's image route when v1 had no image route
	var n int
	im.tx.QueryRow(ctx, `SELECT count(*) FROM stage_route WHERE stage='image' AND tier='*'`).Scan(&n)
	if n == 0 {
		var list []int64
		if err := im.tx.QueryRow(ctx, `SELECT COALESCE(array_agg(c.id ORDER BY c.priority, c.id), '{}') FROM service_config c
			JOIN v1_import m ON m.kind='config' AND m.new_id=c.id WHERE c.kind='image' AND c.enabled`).Scan(&list); err != nil {
			return err
		}
		if len(list) > 0 {
			if _, err := im.tx.Exec(ctx, `INSERT INTO stage_route (stage, tier, config_ids) VALUES ('image','*',$1)`, list); err != nil {
				return err
			}
			im.rep.imported["route"]++
		}
	}
	return nil
}

func (im *importer) accounts(ctx context.Context) error {
	rows, err := im.v1.Query(ctx, `SELECT a.id, a.user_id, COALESCE(a.name,''), COALESCE(a.app_id,''), COALESCE(a.secret_encrypted,''),
		COALESCE(a.subject_type,'personal'), COALESCE(a.verified,0), COALESCE(a.bind_status,''), COALESCE(a.unbound_at,''), COALESCE(a.bound_at,''),
		COALESCE(a.audience,''), COALESCE(a.tone,''), COALESCE(a.taboos,''), COALESCE(a.signatures,''), COALESCE(a.persona_columns,''),
		COALESCE((SELECT p.direction FROM writing_plan p WHERE p.wx_account_id=a.id AND COALESCE(p.direction,'')<>''
		          ORDER BY (p.status='active') DESC, p.id DESC LIMIT 1),'')
		FROM wx_account a ORDER BY a.id`)
	if err != nil {
		return err
	}
	type a struct {
		id, uid                                                                int64
		name, appID, token, subject                                            string
		verified                                                               int
		status, unboundAt, boundAt, audience, tone, taboos, sigs, persona, dir string
	}
	var list []a
	for rows.Next() {
		var x a
		if err := rows.Scan(&x.id, &x.uid, &x.name, &x.appID, &x.token, &x.subject, &x.verified, &x.status, &x.unboundAt, &x.boundAt,
			&x.audience, &x.tone, &x.taboos, &x.sigs, &x.persona, &x.dir); err != nil {
			rows.Close()
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		if _, ok, err := im.mapped(ctx, "account", x.id); err != nil {
			return err
		} else if ok {
			im.rep.already["account"]++
			continue
		}
		uid, ok, err := im.mapped(ctx, "user", x.uid)
		if err != nil {
			return err
		}
		if !ok {
			im.rep.skip("account", x.id, "owner not imported")
			continue
		}
		var role string
		im.tx.QueryRow(ctx, `SELECT role FROM users WHERE id=$1`, uid).Scan(&role)
		if role != "user" {
			im.rep.skip("account", x.id, "owner is staff; staff can't hold accounts in v2")
			continue
		}
		sealed, mask, err := im.reseal(x.token)
		if err != nil {
			im.rep.skip("account", x.id, "cannot decrypt AppSecret: "+err.Error())
			continue
		}
		bind := "bound"
		var unbound *time.Time
		if x.status != "active" && x.status != "bound" {
			bind = "unbound"
			t := parseTime(x.unboundAt)
			unbound = &t
		}
		subject := x.subject
		if subject != "enterprise" {
			subject = "personal"
		}
		var id int64
		err = im.tx.QueryRow(ctx, `INSERT INTO wx_account (user_id, name, app_id, secret_cipher, secret_mask, subject_type, verified, bind_status, unbound_at, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, uid, x.name, x.appID, sealed, mask, subject, x.verified == 1, bind, unbound, parseTime(x.boundAt)).Scan(&id)
		if err != nil {
			if strings.Contains(err.Error(), "wx_account_app_bound_uq") {
				im.rep.skip("account", x.id, "AppID already bound in v2")
				continue
			}
			return err
		}
		sig := firstString(x.sigs)
		if _, err := im.tx.Exec(ctx, `INSERT INTO account_profile (account_id, positioning, audience, tone, taboos, signature) VALUES ($1,$2,$3,$4,$5,$6)`,
			id, x.dir, x.audience, x.tone, x.taboos, sig); err != nil {
			return err
		}
		if strings.TrimSpace(x.persona) != "" && x.persona != "[]" && x.persona != "{}" {
			im.rep.skip("account", x.id, "per-column personas not carried over (v2 has one persona per account); set it in 写作设置")
		}
		if err := im.record(ctx, "account", x.id, id); err != nil {
			return err
		}
	}
	return nil
}

// firstString takes a plain string, or the first string of a JSON array / object.
func firstString(s string) string {
	s = strings.TrimSpace(s)
	var arr []any
	if json.Unmarshal([]byte(s), &arr) == nil {
		for _, v := range arr {
			if t, ok := v.(string); ok && t != "" {
				return t
			}
		}
		return ""
	}
	var obj map[string]any
	if json.Unmarshal([]byte(s), &obj) == nil {
		for _, v := range obj {
			if t, ok := v.(string); ok && t != "" {
				return t
			}
		}
		return ""
	}
	return s
}

var typeDefaults = map[string]struct {
	name string
	news bool
	sort int
}{
	"news": {"行业快报", true, 10}, "steps": {"步骤拆解", false, 20}, "qa": {"问答", false, 30}, "custom": {"故事随笔", false, 40},
}

func (im *importer) typeID(ctx context.Context, key string) (int64, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		key = "custom"
	}
	var id int64
	err := im.tx.QueryRow(ctx, `SELECT id FROM article_type WHERE key=$1`, key).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	d, ok := typeDefaults[key]
	if !ok {
		d.name, d.sort = key, 90
	}
	err = im.tx.QueryRow(ctx, `INSERT INTO article_type (key, name, needs_news, sort) VALUES ($1,$2,$3,$4) RETURNING id`, key, d.name, d.news, d.sort).Scan(&id)
	if err == nil {
		im.rep.imported["type"]++
	}
	return id, err
}

func (im *importer) columns(ctx context.Context) error {
	rows, err := im.v1.Query(ctx, `SELECT id, wx_account_id, COALESCE(name,''), COALESCE(positioning,''), COALESCE(structure,''), COALESCE(word_target,0),
		COALESCE(weight,1), COALESCE(status,'active') FROM account_column ORDER BY wx_account_id, sort_order, id`)
	if err != nil {
		return err
	}
	type c struct {
		id, acc                   int64
		name, note, structure, st string
		words, weight             int
	}
	var list []c
	for rows.Next() {
		var x c
		if err := rows.Scan(&x.id, &x.acc, &x.name, &x.note, &x.structure, &x.words, &x.weight, &x.st); err != nil {
			rows.Close()
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		if _, ok, err := im.mapped(ctx, "column", x.id); err != nil {
			return err
		} else if ok {
			im.rep.already["column"]++
			continue
		}
		acc, ok, err := im.mapped(ctx, "account", x.acc)
		if err != nil {
			return err
		}
		if !ok {
			im.rep.skip("column", x.id, "account not imported")
			continue
		}
		tid, err := im.typeID(ctx, x.structure)
		if err != nil {
			return err
		}
		var hint *int
		if x.words > 0 {
			hint = &x.words
		}
		var id int64
		if err := im.tx.QueryRow(ctx, `INSERT INTO account_column (account_id, name, article_type_id, note, length_hint, weight, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, acc, x.name, tid, x.note, hint, max(1, x.weight), x.st == "active").Scan(&id); err != nil {
			return err
		}
		if err := im.record(ctx, "column", x.id, id); err != nil {
			return err
		}
	}
	return nil
}

var windowTime = map[string]int{"morning": 8 * 60, "noon": 12 * 60, "afternoon": 15 * 60, "evening": 19 * 60, "night": 21 * 60}

func (im *importer) plans(ctx context.Context) error {
	rows, err := im.v1.Query(ctx, `SELECT id, user_id, wx_account_id, COALESCE(schedule_type,''), COALESCE(weekdays,''), COALESCE(time_window,''),
		COALESCE(status,''), COALESCE(daily_count,1) FROM writing_plan ORDER BY id`)
	if err != nil {
		return err
	}
	type p struct {
		id, uid, acc         int64
		sched, days, win, st string
		count                int
	}
	var list []p
	for rows.Next() {
		var x p
		if err := rows.Scan(&x.id, &x.uid, &x.acc, &x.sched, &x.days, &x.win, &x.st, &x.count); err != nil {
			rows.Close()
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		if _, ok, err := im.mapped(ctx, "plan", x.id); err != nil {
			return err
		} else if ok {
			im.rep.already["plan"]++
			continue
		}
		uid, ok1, err := im.mapped(ctx, "user", x.uid)
		if err != nil {
			return err
		}
		acc, ok2, err := im.mapped(ctx, "account", x.acc)
		if err != nil {
			return err
		}
		if !ok1 || !ok2 {
			im.rep.skip("plan", x.id, "user or account not imported")
			continue
		}
		var days []int16
		if x.sched == "daily" {
			days = []int16{1, 2, 3, 4, 5, 6, 7}
		} else {
			var raw []int
			json.Unmarshal([]byte(x.days), &raw)
			for _, d := range raw {
				if d >= 1 && d <= 7 {
					days = append(days, int16(d))
				}
			}
			if len(days) == 0 {
				days = []int16{1, 2, 3, 4, 5}
				im.rep.skip("plan", x.id, "weekdays unreadable; imported as Mon–Fri (disabled)")
			}
		}
		at, ok := windowTime[x.win]
		if !ok {
			at = 8 * 60
		}
		if x.count > 1 {
			im.rep.skip("plan", x.id, fmt.Sprintf("v1 wrote %d a day; v2 plans write one a day, add more plans if needed", x.count))
		}
		was := map[string]string{"active": "原为启用", "paused": "原为暂停"}[x.st]
		var id int64
		if err := im.tx.QueryRow(ctx, `INSERT INTO write_plan (user_id, account_id, weekdays, at_minute, enabled, last_result)
			VALUES ($1,$2,$3,$4,FALSE,$5) RETURNING id`, uid, acc, days, at, strings.TrimSpace("从旧系统导入，已停用 "+was)).Scan(&id); err != nil {
			return err
		}
		if err := im.record(ctx, "plan", x.id, id); err != nil {
			return err
		}
	}
	return nil
}
