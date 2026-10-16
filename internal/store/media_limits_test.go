package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/security"
)

func TestMediaJobScopesReserveAndTransferAtomically(t *testing.T) {
	master, _ := security.GenerateMasterKey()
	encryptor, _ := security.NewEncryptor(master)
	data, err := OpenSQLite(t.TempDir()+"/media.db", encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if _, _, err := data.CreateAPIKey(ctx, id, id, nil, config.ClientKeyPolicy{Team: "team"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []MediaJobScope{{Kind: "global", Limit: 2}, {Kind: "team", ID: "team", Limit: 2}, {Kind: "key", ID: "a", Limit: 2}, {Kind: "provider", ID: "p", Limit: 2}} {
		t.Run(scope.Kind, func(t *testing.T) {
			var wg sync.WaitGroup
			accepted := make(chan *MediaJobReservation, 20)
			for range 20 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					token, err := data.ReserveMediaJob(ctx, MediaJobOwner{ProviderID: "p", ClientAPIKeyID: "a", Team: "team"}, []MediaJobScope{scope})
					if err == nil {
						accepted <- token
					} else if _, ok := err.(*MediaJobLimitError); !ok {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			close(accepted)
			if len(accepted) != 2 {
				t.Fatalf("admitted %d", len(accepted))
			}
			index := 0
			for token := range accepted {
				job := MediaJob{ID: fmt.Sprintf("%s-%d", scope.Kind, index), Kind: "video", Status: "queued", ProviderID: "p", ClientAPIKeyID: "a"}
				if err := token.Save(ctx, job); err != nil {
					t.Fatal(err)
				}
				token.Release()
				token.Release()
				index++
			}
			if n, err := data.ActiveMediaJobsInScope(ctx, scope); err != nil || n != 2 {
				t.Fatalf("transferred count=%d, %v", n, err)
			}
			if _, err := data.ReserveMediaJob(ctx, MediaJobOwner{ProviderID: "p", ClientAPIKeyID: "a", Team: "team"}, []MediaJobScope{scope}); err == nil {
				t.Fatal("persisted job stopped counting")
			}
			for i := range 2 {
				if err := data.UpdateMediaJob(ctx, fmt.Sprintf("%s-%d", scope.Kind, i), "completed", "{}", "", "", time.Time{}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
