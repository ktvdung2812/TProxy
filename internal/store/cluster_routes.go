package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tproxy/tproxy/internal/config"
)

const clusterPendingRoutesKey = "cluster.pending_routes"

// saveModelRoutesTx shares the route replacement logic used by model saves and
// attachments. Local-only replacement leaves cluster routes untouched.
func saveModelRoutesTx(ctx context.Context, tx *sql.Tx, model string, routes []config.RouteTargetConfig, localOnly bool) error {
	query := `DELETE FROM route_targets WHERE public_model_id=?`
	args := []any{model}
	if localOnly {
		query += ` AND provider_id<>? AND provider_id NOT IN (SELECT id FROM providers WHERE type=?)`
		args = append(args, ClusterProviderID, ClusterProviderID)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	used := map[string]struct{}{}
	for _, route := range routes {
		if localOnly && route.Provider == ClusterProviderID {
			return fmt.Errorf("cluster route for %q cannot use provider %q", model, route.Provider)
		}
		id := allocateRouteTargetID(model, route, used)
		if route.Weight <= 0 {
			route.Weight = 1
		}
		conditions, err := json.Marshal(routeConditions(route))
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO route_targets(id,public_model_id,provider_id,upstream_model,priority,weight,enabled,conditions_json) VALUES(?,?,?,?,?,?,?,?)`, id, model, route.Provider, route.UpstreamModel, route.Priority, route.Weight, boolInt(route.IsEnabled()), string(conditions)); err != nil {
			return err
		}
	}
	return nil
}

// applyClusterRoutesTx merges new attachments with pending ones, applying any
// whose model exists. A failed write rolls back the catalog or seed transaction.
func applyClusterRoutesTx(ctx context.Context, tx *sql.Tx, incoming []config.ClusterRouteAttachment) error {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key=?`, clusterPendingRoutesKey).Scan(&raw)
	var pending []config.ClusterRouteAttachment
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &pending); err != nil {
			return fmt.Errorf("decode pending cluster routes: %w", err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	} else if len(incoming) == 0 {
		return nil
	}
	for _, attachment := range incoming {
		found := false
		for i := range pending {
			if pending[i].Model == attachment.Model {
				pending[i], found = attachment, true
				break
			}
		}
		if !found {
			pending = append(pending, attachment)
		}
	}
	remaining := make([]config.ClusterRouteAttachment, 0, len(pending))
	for _, attachment := range pending {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public_models WHERE id=?)`, attachment.Model).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			remaining = append(remaining, attachment)
			continue
		}
		if err = saveModelRoutesTx(ctx, tx, attachment.Model, attachment.Routes, true); err != nil {
			return fmt.Errorf("apply cluster routes for %s: %w", attachment.Model, err)
		}
	}
	if len(remaining) == 0 {
		_, err = tx.ExecContext(ctx, `DELETE FROM app_settings WHERE key=?`, clusterPendingRoutesKey)
		return err
	}
	encoded, err := json.Marshal(remaining)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO app_settings(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, clusterPendingRoutesKey, string(encoded))
	return err
}
