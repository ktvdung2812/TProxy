package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/tproxy/tproxy/internal/config"
)

type ClusterCatalogModel struct {
	ID, DisplayName                                   string
	Aliases                                           []string
	Enabled, ExposeUpstreamName, RewriteResponseModel bool
	Capabilities                                      []string
	Limits, Policy                                    map[string]any
	Pricing                                           *config.PricingConfig
}

type ClusterCatalogComboItem struct {
	Position      int
	PublicModelID string
}

type ClusterCatalogCombo struct {
	ID, DisplayName               string
	Enabled, RewriteResponseModel bool
	Capabilities                  []string
	Limits, Policy                map[string]any
	Items                         []ClusterCatalogComboItem
}

type ClusterCatalogAPIKey struct {
	ID, Name, KeyHash string
	Enabled           bool
	Models            []string
	Policy            config.ClientKeyPolicy
}

type ClusterCatalog struct {
	Models  []ClusterCatalogModel
	Combos  []ClusterCatalogCombo
	APIKeys []ClusterCatalogAPIKey
}

// ClusterCatalogConflict is a catalog entry a member skipped because a local
// row already owns the name. Kind is model, alias, combo or api_key.
type ClusterCatalogConflict struct {
	Kind, ID, Reason string
}

type ClusterApplyReport struct {
	Models, Combos, APIKeys []string
	Conflicts               []ClusterCatalogConflict
}

type ClusterManaged struct {
	Models, Aliases, Combos, APIKeys []string
}

const (
	clusterKindModel  = "model"
	clusterKindAlias  = "alias"
	clusterKindCombo  = "combo"
	clusterKindAPIKey = "api_key"
)

// BuildClusterCatalog collects what a hub shares. Unknown ids are skipped, a
// combo is shared only when every item is a shared model, and API keys are
// narrowed to the shared models and stripped of hub-local policy.
func (s *Store) BuildClusterCatalog(ctx context.Context, shared ClusterShared) (ClusterCatalog, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ClusterCatalog{}, err
	}
	defer func() { _ = tx.Rollback() }()
	catalog := ClusterCatalog{Models: []ClusterCatalogModel{}, Combos: []ClusterCatalogCombo{}, APIKeys: []ClusterCatalogAPIKey{}}
	// Combo items may name a model through a global alias.
	modelByName := map[string]string{}
	for _, id := range clusterIDs(shared.Models) {
		model, found, err := clusterCatalogModelTx(ctx, tx, id)
		if err != nil {
			return ClusterCatalog{}, err
		}
		if !found {
			continue
		}
		if price, ok := shared.Prices[id]; ok {
			model.Pricing = &price
		}
		modelByName[id] = id
		for _, alias := range model.Aliases {
			modelByName[alias] = id
		}
		catalog.Models = append(catalog.Models, model)
	}
	allowed := map[string]bool{}
	for _, model := range catalog.Models {
		allowed[model.ID] = true
	}
	for _, id := range clusterIDs(shared.Combos) {
		combo, found, err := clusterCatalogComboTx(ctx, tx, id, modelByName)
		if err != nil {
			return ClusterCatalog{}, err
		}
		if found {
			catalog.Combos = append(catalog.Combos, combo)
			allowed[id] = true
		}
	}
	for _, id := range clusterIDs(shared.APIKeys) {
		var key ClusterCatalogAPIKey
		var models, policy string
		var enabled int
		err := tx.QueryRowContext(ctx, `SELECT id,name,key_hash,models_json,policy_json,enabled FROM api_keys WHERE id=?`, id).Scan(&key.ID, &key.Name, &key.KeyHash, &models, &policy, &enabled)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return ClusterCatalog{}, err
		}
		var requested []string
		_ = json.Unmarshal([]byte(models), &requested)
		_ = json.Unmarshal([]byte(policy), &key.Policy)
		key.Enabled = enabled != 0
		key.Models = clusterKeyModels(requested, allowed)
		key.Policy = clusterKeyPolicy(key.Policy)
		catalog.APIKeys = append(catalog.APIKeys, key)
	}
	return catalog, nil
}

func clusterCatalogModelTx(ctx context.Context, tx *sql.Tx, id string) (ClusterCatalogModel, bool, error) {
	model := ClusterCatalogModel{ID: id}
	var enabled, expose, rewrite int
	var capabilities, limits string
	err := tx.QueryRowContext(ctx, `SELECT display_name,enabled,expose_upstream_name,rewrite_response_model,capabilities_json,limits_json FROM public_models WHERE id=?`, id).Scan(&model.DisplayName, &enabled, &expose, &rewrite, &capabilities, &limits)
	if errors.Is(err, sql.ErrNoRows) {
		return ClusterCatalogModel{}, false, nil
	}
	if err != nil {
		return ClusterCatalogModel{}, false, err
	}
	model.Enabled, model.ExposeUpstreamName, model.RewriteResponseModel = enabled != 0, expose != 0, rewrite != 0
	_ = json.Unmarshal([]byte(capabilities), &model.Capabilities)
	_ = json.Unmarshal([]byte(limits), &model.Limits)
	// The model's own id is implied, and key- or team-scoped aliases are
	// hub-local policy.
	aliases, err := queryClusterStrings(ctx, tx, `SELECT alias FROM model_aliases WHERE public_model_id=? AND scope_api_key_id='' AND scope_team_id='' AND enabled=1 AND alias<>? ORDER BY alias`, id, id)
	if err != nil {
		return ClusterCatalogModel{}, false, err
	}
	model.Aliases = append([]string{}, aliases...)
	return model, true, nil
}

func clusterCatalogComboTx(ctx context.Context, tx *sql.Tx, id string, modelByName map[string]string) (ClusterCatalogCombo, bool, error) {
	combo := ClusterCatalogCombo{ID: id}
	var enabled, rewrite int
	var capabilities, limits, policy string
	err := tx.QueryRowContext(ctx, `SELECT display_name,enabled,rewrite_response_model,capabilities_json,limits_json,policy_json FROM combos WHERE id=?`, id).Scan(&combo.DisplayName, &enabled, &rewrite, &capabilities, &limits, &policy)
	if errors.Is(err, sql.ErrNoRows) {
		return ClusterCatalogCombo{}, false, nil
	}
	if err != nil {
		return ClusterCatalogCombo{}, false, err
	}
	combo.Enabled, combo.RewriteResponseModel = enabled != 0, rewrite != 0
	_ = json.Unmarshal([]byte(capabilities), &combo.Capabilities)
	_ = json.Unmarshal([]byte(limits), &combo.Limits)
	_ = json.Unmarshal([]byte(policy), &combo.Policy)
	rows, err := tx.QueryContext(ctx, `SELECT position,public_model_id FROM combo_items WHERE combo_id=? ORDER BY position`, id)
	if err != nil {
		return ClusterCatalogCombo{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ClusterCatalogComboItem
		if err = rows.Scan(&item.Position, &item.PublicModelID); err != nil {
			return ClusterCatalogCombo{}, false, err
		}
		combo.Items = append(combo.Items, item)
	}
	if err = rows.Err(); err != nil {
		return ClusterCatalogCombo{}, false, err
	}
	if len(combo.Items) == 0 {
		return ClusterCatalogCombo{}, false, nil
	}
	for index, item := range combo.Items {
		modelID, shared := modelByName[item.PublicModelID]
		if !shared {
			return ClusterCatalogCombo{}, false, nil
		}
		combo.Items[index].PublicModelID = modelID
	}
	return combo, true, nil
}

// clusterKeyModels narrows a key's model list to allowed ids and expands "*"
// to all of them. The result is never nil, because an empty list is the
// explicit deny-all policy.
func clusterKeyModels(requested []string, allowed map[string]bool) []string {
	result := []string{}
	seen := map[string]bool{}
	add := func(id string) {
		if allowed[id] && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	for _, id := range requested {
		if id != "*" {
			add(id)
			continue
		}
		every := make([]string, 0, len(allowed))
		for allowedID := range allowed {
			every = append(every, allowedID)
		}
		sort.Strings(every)
		for _, allowedID := range every {
			add(allowedID)
		}
	}
	return result
}

// clusterKeyPolicy drops the parts of a key policy that name things which only
// exist on the node that owns them.
func clusterKeyPolicy(policy config.ClientKeyPolicy) config.ClientKeyPolicy {
	policy.CredentialIDs, policy.Team, policy.Tags = nil, "", nil
	return policy
}

// ApplyClusterCatalog makes the managed rows of a member match catalog in one
// transaction. Local rows always win: an entry whose name is taken by an
// unmanaged row is skipped and reported as a conflict. Managed rows the catalog
// no longer carries are removed, except that a model the member attached local
// routes to is kept as a local model.
func (s *Store) ApplyClusterCatalog(ctx context.Context, catalog ClusterCatalog) (ClusterApplyReport, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ClusterApplyReport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	managed, err := loadClusterManaged(ctx, tx)
	if err != nil {
		return ClusterApplyReport{}, err
	}
	incoming := clusterCatalogNames(catalog)
	// Remove what the hub stopped sharing first, so those names are free again.
	if err = removeClusterManagedTx(ctx, tx, managed, incoming.has); err != nil {
		return ClusterApplyReport{}, err
	}
	apply := &clusterCatalogApply{ctx: ctx, tx: tx, now: now, managed: managed, incoming: incoming, applied: clusterManagedSet{}, aliasOwner: map[string]string{},
		report: ClusterApplyReport{Models: []string{}, Combos: []string{}, APIKeys: []string{}, Conflicts: []ClusterCatalogConflict{}}}
	for _, model := range catalog.Models {
		if err = apply.model(model); err != nil {
			return ClusterApplyReport{}, fmt.Errorf("apply cluster model %s: %w", model.ID, err)
		}
	}
	for _, combo := range catalog.Combos {
		if err = apply.combo(combo); err != nil {
			return ClusterApplyReport{}, fmt.Errorf("apply cluster combo %s: %w", combo.ID, err)
		}
	}
	for _, key := range catalog.APIKeys {
		if err = apply.apiKey(key); err != nil {
			return ClusterApplyReport{}, fmt.Errorf("apply cluster api key %s: %w", key.ID, err)
		}
	}
	// A managed entry the catalog still carries but that could not be applied
	// this time must not linger in its old form.
	if err = removeClusterManagedTx(ctx, tx, managed, apply.applied.has); err != nil {
		return ClusterApplyReport{}, err
	}
	if err = applyClusterRoutesTx(ctx, tx, nil); err != nil {
		return ClusterApplyReport{}, err
	}
	if err = tx.Commit(); err != nil {
		return ClusterApplyReport{}, err
	}
	return apply.report, nil
}

func (s *Store) ClusterManagedItems(ctx context.Context) (ClusterManaged, error) {
	managed, err := loadClusterManaged(ctx, s.db)
	if err != nil {
		return ClusterManaged{}, err
	}
	return ClusterManaged{Models: managed.ids(clusterKindModel), Aliases: managed.ids(clusterKindAlias), Combos: managed.ids(clusterKindCombo), APIKeys: managed.ids(clusterKindAPIKey)}, nil
}

// IsClusterManaged reports whether a row belongs to the cluster. Providers and
// credentials are cluster rows when they are (under) the tproxy-cluster
// provider; the other kinds are looked up in cluster_managed_items.
func (s *Store) IsClusterManaged(ctx context.Context, kind, id string) (bool, error) {
	var query string
	args := []any{id, ClusterProviderID}
	switch kind {
	case clusterKindModel, clusterKindAlias, clusterKindCombo, clusterKindAPIKey:
		query, args = `SELECT COUNT(*) FROM cluster_managed_items WHERE kind=? AND item_id=?`, []any{kind, id}
	case "provider":
		query = `SELECT COUNT(*) FROM providers WHERE id=? AND type=?`
	case "credential":
		query = `SELECT COUNT(*) FROM credentials c JOIN providers p ON p.id=c.provider_id WHERE c.id=? AND p.type=?`
	default:
		return false, fmt.Errorf("unknown cluster managed kind %q", kind)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// ClearClusterManaged removes every managed row as if an empty catalog had been
// applied, then forgets all managed markers.
func (s *Store) ClearClusterManaged(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	managed, err := loadClusterManaged(ctx, tx)
	if err != nil {
		return err
	}
	if err = removeClusterManagedTx(ctx, tx, managed, func(string, string) bool { return false }); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM cluster_managed_items`); err != nil {
		return err
	}
	return tx.Commit()
}

// ClusterLendingSummary aggregates this node's usage by cluster borrowers per
// UTC day since sinceDay (YYYY-MM-DD; empty for all). Relayed requests run as
// "cluster:<origin node>" and become borrower "node:<id>"; requests made with
// a hub-synced client key become "key:<id>". Lender is left for the caller.
func (s *Store) ClusterLendingSummary(ctx context.Context, sinceDay string) ([]ClusterLedgerRow, error) {
	if err := validateClusterDay(sinceDay); err != nil {
		return nil, err
	}
	// Comparing against the bare day keeps every timestamp of that day, which
	// a full RFC3339Nano midnight would not: "…T00:00:00.5Z" sorts before
	// "…T00:00:00Z".
	rows, err := s.db.QueryContext(ctx, `SELECT day,borrower,COUNT(DISTINCT request_id),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(estimated_cost_usd),0)
FROM (SELECT substr(created_at,1,10) AS day, request_id, input_tokens, output_tokens, estimated_cost_usd,
CASE WHEN cluster_borrower <> '' THEN cluster_borrower
     WHEN client_api_key_id LIKE 'cluster:%' THEN 'node:' || substr(client_api_key_id,9)
     ELSE 'key:' || client_api_key_id END AS borrower
FROM usage_events WHERE created_at>=? AND (cluster_borrower <> '' OR (cluster_borrower = '' AND (client_api_key_id LIKE 'cluster:%' OR client_api_key_id IN (SELECT item_id FROM cluster_managed_items WHERE kind=?)))))
GROUP BY day,borrower`, sinceDay, clusterKindAPIKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summary := []ClusterLedgerRow{}
	for rows.Next() {
		var row ClusterLedgerRow
		if err := rows.Scan(&row.Day, &row.Borrower, &row.Requests, &row.InputTokens, &row.OutputTokens, &row.CostUSD); err != nil {
			return nil, err
		}
		summary = append(summary, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(summary, func(i, j int) bool {
		if summary[i].Day != summary[j].Day {
			return summary[i].Day > summary[j].Day
		}
		return summary[i].Borrower < summary[j].Borrower
	})
	return summary, nil
}

// clusterManagedSet maps a kind to the ids of that kind.
type clusterManagedSet map[string]map[string]bool

func (set clusterManagedSet) has(kind, id string) bool { return set[kind][id] }

func (set clusterManagedSet) add(kind, id string) {
	if set[kind] == nil {
		set[kind] = map[string]bool{}
	}
	set[kind][id] = true
}

func (set clusterManagedSet) ids(kind string) []string {
	ids := make([]string, 0, len(set[kind]))
	for id := range set[kind] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// unmanagedAliases drops hub-owned names from a local model's alias list.
func (set clusterManagedSet) unmanagedAliases(aliases []string) []string {
	var result []string
	for _, alias := range aliases {
		if !set.has(clusterKindAlias, alias) {
			result = append(result, alias)
		}
	}
	return result
}

func loadClusterManaged(ctx context.Context, q clusterQueryer) (clusterManagedSet, error) {
	rows, err := q.QueryContext(ctx, `SELECT kind,item_id FROM cluster_managed_items`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := clusterManagedSet{}
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			return nil, err
		}
		set.add(kind, id)
	}
	return set, rows.Err()
}

func clusterCatalogNames(catalog ClusterCatalog) clusterManagedSet {
	names := clusterManagedSet{}
	for _, model := range catalog.Models {
		names.add(clusterKindModel, model.ID)
		for _, alias := range model.Aliases {
			names.add(clusterKindAlias, alias)
		}
	}
	for _, combo := range catalog.Combos {
		names.add(clusterKindCombo, combo.ID)
	}
	for _, key := range catalog.APIKeys {
		names.add(clusterKindAPIKey, key.ID)
	}
	return names
}

// removeClusterManagedTx deletes managed rows that keep rejects and forgets
// them. A model with local route targets stays as a local model together with
// its aliases; only aliases the catalog still claims stay managed so the apply
// step can move them to their new model.
func removeClusterManagedTx(ctx context.Context, tx *sql.Tx, managed clusterManagedSet, keep func(kind, id string) bool) error {
	exec := func(query string, args ...any) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}
	forget := func(kind, id string) error {
		delete(managed[kind], id)
		return exec(`DELETE FROM cluster_managed_items WHERE kind=? AND item_id=?`, kind, id)
	}
	for _, id := range managed.ids(clusterKindAPIKey) {
		if keep(clusterKindAPIKey, id) {
			continue
		}
		if err := exec(`DELETE FROM api_keys WHERE id=?`, id); err != nil {
			return err
		}
		if err := forget(clusterKindAPIKey, id); err != nil {
			return err
		}
	}
	for _, id := range managed.ids(clusterKindCombo) {
		if keep(clusterKindCombo, id) {
			continue
		}
		if err := exec(`DELETE FROM combo_items WHERE combo_id=?`, id); err != nil {
			return err
		}
		if err := exec(`DELETE FROM combos WHERE id=?`, id); err != nil {
			return err
		}
		if err := forget(clusterKindCombo, id); err != nil {
			return err
		}
	}
	for _, id := range managed.ids(clusterKindModel) {
		if keep(clusterKindModel, id) {
			continue
		}
		aliases, err := queryClusterStrings(ctx, tx, `SELECT alias FROM model_aliases WHERE public_model_id=? AND scope_api_key_id='' AND scope_team_id=''`, id)
		if err != nil {
			return err
		}
		var routes int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM route_targets WHERE public_model_id=?`, id).Scan(&routes); err != nil {
			return err
		}
		if routes == 0 {
			// Same cleanup as DeletePublicModel, without relying on the
			// foreign-key cascade being enabled on this connection.
			for _, statement := range []string{`DELETE FROM combo_items WHERE public_model_id=?`, `DELETE FROM model_aliases WHERE public_model_id=?`, `DELETE FROM public_models WHERE id=?`} {
				if err = exec(statement, id); err != nil {
					return err
				}
			}
		}
		if err = forget(clusterKindModel, id); err != nil {
			return err
		}
		for _, alias := range aliases {
			if !managed.has(clusterKindAlias, alias) || (routes > 0 && keep(clusterKindAlias, alias)) {
				continue
			}
			if err = forget(clusterKindAlias, alias); err != nil {
				return err
			}
		}
	}
	for _, alias := range managed.ids(clusterKindAlias) {
		if keep(clusterKindAlias, alias) {
			continue
		}
		if err := exec(`DELETE FROM model_aliases WHERE alias=? AND scope_api_key_id='' AND scope_team_id=''`, alias); err != nil {
			return err
		}
		if err := forget(clusterKindAlias, alias); err != nil {
			return err
		}
	}
	return nil
}

type clusterCatalogApply struct {
	ctx        context.Context
	tx         *sql.Tx
	now        string
	managed    clusterManagedSet // rows this node manages, kept current while applying
	incoming   clusterManagedSet // names the catalog uses
	applied    clusterManagedSet
	aliasOwner map[string]string // aliases claimed earlier in this apply
	allowed    map[string]bool   // applied models and combos, for key model lists
	report     ClusterApplyReport
}

func (a *clusterCatalogApply) model(model ClusterCatalogModel) error {
	id := model.ID
	if id == "" || a.applied.has(clusterKindModel, id) {
		return nil
	}
	reason, err := a.modelNameTaken(id)
	if err != nil {
		return err
	}
	if reason != "" {
		a.conflict(clusterKindModel, id, reason)
		return nil
	}
	aliases := []string{}
	for _, alias := range clusterIDs(model.Aliases) {
		if alias == id {
			continue
		}
		taken, err := a.aliasTaken(alias, id)
		if err != nil {
			return err
		}
		if taken {
			a.conflict(clusterKindAlias, alias, "alias_in_use")
			continue
		}
		aliases = append(aliases, alias)
	}
	encodedAliases, _ := json.Marshal(aliases)
	capabilities, _ := json.Marshal(model.Capabilities)
	limits, _ := json.Marshal(model.Limits)
	if _, err = a.tx.ExecContext(a.ctx, `INSERT INTO public_models(id,display_name,aliases_json,enabled,expose_upstream_name,rewrite_response_model,capabilities_json,limits_json) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,aliases_json=excluded.aliases_json,enabled=excluded.enabled,expose_upstream_name=excluded.expose_upstream_name,rewrite_response_model=excluded.rewrite_response_model,capabilities_json=excluded.capabilities_json,limits_json=excluded.limits_json`,
		id, model.DisplayName, string(encodedAliases), boolInt(model.Enabled), boolInt(model.ExposeUpstreamName), boolInt(model.RewriteResponseModel), string(capabilities), string(limits)); err != nil {
		return err
	}
	// Like SavePublicModel, the model's own id is also a global alias row. It
	// lives and dies with the model, so it is not tracked separately.
	for _, alias := range append([]string{id}, aliases...) {
		owner, err := a.globalAliasOwner(alias)
		if err != nil {
			return err
		}
		if owner != "" && owner != id {
			var encoded string
			if err = a.tx.QueryRowContext(a.ctx, `SELECT aliases_json FROM public_models WHERE id=?`, owner).Scan(&encoded); err != nil {
				return err
			}
			var oldAliases []string
			if err = json.Unmarshal([]byte(encoded), &oldAliases); err != nil {
				return err
			}
			filtered := make([]string, 0, len(oldAliases))
			for _, old := range oldAliases {
				if old != alias {
					filtered = append(filtered, old)
				}
			}
			updated, _ := json.Marshal(filtered)
			if _, err = a.tx.ExecContext(a.ctx, `UPDATE public_models SET aliases_json=? WHERE id=?`, string(updated), owner); err != nil {
				return err
			}
		}
		if _, err = a.tx.ExecContext(a.ctx, `INSERT INTO model_aliases(alias,public_model_id,scope_api_key_id,scope_team_id,enabled) VALUES(?,?,'','',1) ON CONFLICT(alias,scope_api_key_id,scope_team_id) DO UPDATE SET public_model_id=excluded.public_model_id,enabled=1`, alias, id); err != nil {
			return err
		}
	}
	if err = a.mark(clusterKindModel, id); err != nil {
		return err
	}
	for _, alias := range aliases {
		a.aliasOwner[alias] = id
		if err = a.mark(clusterKindAlias, alias); err != nil {
			return err
		}
	}
	a.report.Models = append(a.report.Models, id)
	return nil
}

// modelNameTaken returns the conflict reason when a local row owns the name.
// Models and combos share one namespace, and a local global alias with the
// same name would otherwise be silently shadowed by the hub's model.
func (a *clusterCatalogApply) modelNameTaken(id string) (string, error) {
	if !a.managed.has(clusterKindModel, id) {
		found, err := a.exists(`SELECT 1 FROM public_models WHERE id=?`, id)
		if err != nil || found {
			return "local_model_exists", err
		}
	}
	found, err := a.exists(`SELECT 1 FROM combos WHERE id=?`, id)
	if err != nil || found {
		return "local_model_exists", err
	}
	owner, err := a.globalAliasOwner(id)
	if err != nil {
		return "", err
	}
	if owner != "" && owner != id {
		return "alias_in_use", nil
	}
	return "", nil
}

func (a *clusterCatalogApply) aliasTaken(alias, modelID string) (bool, error) {
	if owner, claimed := a.aliasOwner[alias]; claimed && owner != modelID {
		return true, nil
	}
	if a.incoming.has(clusterKindModel, alias) || a.incoming.has(clusterKindCombo, alias) {
		return true, nil
	}
	for _, query := range []string{`SELECT 1 FROM public_models WHERE id=?`, `SELECT 1 FROM combos WHERE id=?`} {
		if found, err := a.exists(query, alias); err != nil || found {
			return found, err
		}
	}
	owner, err := a.globalAliasOwner(alias)
	if err != nil || owner == "" {
		return false, err
	}
	// A managed alias may move to another model; a local one is never taken over.
	return !a.managed.has(clusterKindAlias, alias), nil
}

func (a *clusterCatalogApply) combo(combo ClusterCatalogCombo) error {
	id := combo.ID
	if id == "" || len(combo.Items) == 0 || a.applied.has(clusterKindCombo, id) {
		return nil
	}
	reason, err := a.comboNameTaken(id)
	if err != nil {
		return err
	}
	if reason != "" {
		a.conflict(clusterKindCombo, id, reason)
		return nil
	}
	items := append([]ClusterCatalogComboItem(nil), combo.Items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Position < items[j].Position })
	for _, item := range items {
		// An item must resolve to the hub's model, never to a local one that
		// happens to share its id.
		if !a.applied.has(clusterKindModel, item.PublicModelID) {
			a.conflict(clusterKindCombo, id, "model_unavailable")
			return nil
		}
	}
	capabilities, _ := json.Marshal(combo.Capabilities)
	limits, _ := json.Marshal(combo.Limits)
	policy, _ := json.Marshal(combo.Policy)
	if _, err = a.tx.ExecContext(a.ctx, `INSERT INTO combos(id,display_name,enabled,rewrite_response_model,capabilities_json,limits_json,policy_json) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,enabled=excluded.enabled,rewrite_response_model=excluded.rewrite_response_model,capabilities_json=excluded.capabilities_json,limits_json=excluded.limits_json,policy_json=excluded.policy_json`,
		id, combo.DisplayName, boolInt(combo.Enabled), boolInt(combo.RewriteResponseModel), string(capabilities), string(limits), string(policy)); err != nil {
		return err
	}
	if _, err = a.tx.ExecContext(a.ctx, `DELETE FROM combo_items WHERE combo_id=?`, id); err != nil {
		return err
	}
	for position, item := range items {
		if _, err = a.tx.ExecContext(a.ctx, `INSERT INTO combo_items(combo_id,position,public_model_id,route_target_id) VALUES(?,?,?,'')`, id, position, item.PublicModelID); err != nil {
			return err
		}
	}
	if err = a.mark(clusterKindCombo, id); err != nil {
		return err
	}
	a.report.Combos = append(a.report.Combos, id)
	return nil
}

func (a *clusterCatalogApply) comboNameTaken(id string) (string, error) {
	if !a.managed.has(clusterKindCombo, id) {
		found, err := a.exists(`SELECT 1 FROM combos WHERE id=?`, id)
		if err != nil || found {
			return "local_combo_exists", err
		}
	}
	found, err := a.exists(`SELECT 1 FROM public_models WHERE id=?`, id)
	if err != nil || found {
		return "local_model_exists", err
	}
	owner, err := a.globalAliasOwner(id)
	if err != nil || owner == "" {
		return "", err
	}
	return "alias_in_use", nil
}

func (a *clusterCatalogApply) apiKey(key ClusterCatalogAPIKey) error {
	id := key.ID
	if id == "" || key.KeyHash == "" || a.applied.has(clusterKindAPIKey, id) {
		return nil
	}
	if !a.managed.has(clusterKindAPIKey, id) {
		found, err := a.exists(`SELECT 1 FROM api_keys WHERE id=?`, id)
		if err != nil {
			return err
		}
		if found {
			a.conflict(clusterKindAPIKey, id, "local_key_exists")
			return nil
		}
	}
	var owner string
	err := a.tx.QueryRowContext(a.ctx, `SELECT id FROM api_keys WHERE key_hash=?`, key.KeyHash).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && owner != id {
		a.conflict(clusterKindAPIKey, id, "local_key_exists")
		return nil
	}
	if a.allowed == nil {
		a.allowed = map[string]bool{}
		for _, kind := range []string{clusterKindModel, clusterKindCombo} {
			for appliedID := range a.applied[kind] {
				a.allowed[appliedID] = true
			}
		}
	}
	// Narrow again on the member: an id that conflicted here names a local model
	// that a hub key must not reach.
	models, _ := json.Marshal(clusterKeyModels(key.Models, a.allowed))
	policy, _ := json.Marshal(clusterKeyPolicy(key.Policy))
	if _, err = a.tx.ExecContext(a.ctx, `INSERT INTO api_keys(id,name,key_hash,models_json,policy_json,enabled) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,key_hash=excluded.key_hash,models_json=excluded.models_json,policy_json=excluded.policy_json,enabled=excluded.enabled`,
		id, key.Name, key.KeyHash, string(models), string(policy), boolInt(key.Enabled)); err != nil {
		return err
	}
	if err = a.mark(clusterKindAPIKey, id); err != nil {
		return err
	}
	a.report.APIKeys = append(a.report.APIKeys, id)
	return nil
}

func (a *clusterCatalogApply) mark(kind, id string) error {
	a.managed.add(kind, id)
	a.applied.add(kind, id)
	_, err := a.tx.ExecContext(a.ctx, `INSERT INTO cluster_managed_items(kind,item_id,updated_at) VALUES(?,?,?) ON CONFLICT(kind,item_id) DO UPDATE SET updated_at=excluded.updated_at`, kind, id, a.now)
	return err
}

func (a *clusterCatalogApply) conflict(kind, id, reason string) {
	a.report.Conflicts = append(a.report.Conflicts, ClusterCatalogConflict{Kind: kind, ID: id, Reason: reason})
}

func (a *clusterCatalogApply) exists(query string, args ...any) (bool, error) {
	var found int
	err := a.tx.QueryRowContext(a.ctx, query, args...).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (a *clusterCatalogApply) globalAliasOwner(alias string) (string, error) {
	var owner string
	err := a.tx.QueryRowContext(a.ctx, `SELECT public_model_id FROM model_aliases WHERE alias=? AND scope_api_key_id='' AND scope_team_id=''`, alias).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return owner, err
}
