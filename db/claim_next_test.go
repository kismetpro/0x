package db

import (
	"fmt"
	"sync"
	"testing"
)

func TestClaimNextIntentConcurrentExactlyOnce(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTask("local concurrent claim", "test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	for i := 0; i < 100; i++ {
		if _, err := store.AddNode(KindIntent, map[string]any{"summary": "test"}, i%3, "open", "test", nil); err != nil {
			t.Fatal(err)
		}
	}
	ids := make(chan int64, 100)
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				n, err := store.ClaimNextIntent(fmt.Sprint(i))
				if err != nil {
					t.Error(err)
					return
				}
				if n == nil {
					return
				}
				if n.State != "running" {
					t.Errorf("state %s", n.State)
				}
				ids <- n.ID
			}
		}(i)
	}
	wg.Wait()
	close(ids)
	seen := map[int64]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate claim: %d", id)
		}
		seen[id] = true
	}
	if len(seen) != 100 {
		t.Fatalf("claimed %d/100", len(seen))
	}
}

func BenchmarkClaimIntent(b *testing.B) {
	for _, atomicClaim := range []bool{false, true} {
		name := "frontier_cas"
		if atomicClaim {
			name = "skip_locked"
		}
		b.Run(name, func(b *testing.B) {
			dsn, _, err := DSN()
			if err != nil {
				b.Skip("no test database configured")
			}
			d, err := Open(dsn)
			if err != nil {
				b.Fatal(err)
			}
			defer d.Close()
			task, err := d.CreateTask("claim benchmark", "local", nil, 0, 0)
			if err != nil {
				b.Fatal(err)
			}
			defer d.DeleteTask(task.ID)
			store := d.Exploration(task.ExplorationID)
			_, err = d.Exec(`INSERT INTO exploration_nodes(exploration_id,kind,payload,priority,state,origin)
				SELECT $1,'intent','{}'::jsonb,0,'open','test' FROM generate_series(1,$2)`, task.ExplorationID, b.N)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if atomicClaim {
					if n, err := store.ClaimNextIntent("bench"); err != nil || n == nil {
						b.Fatalf("%v %v", n, err)
					}
				} else {
					fr, err := store.Frontier(20)
					if err != nil || len(fr) == 0 {
						b.Fatalf("frontier: %v", err)
					}
					if ok, err := store.ClaimIntent(fr[0].ID, "bench"); err != nil || !ok {
						b.Fatalf("claim: %v", err)
					}
				}
			}
			b.StopTimer()
		})
	}
}
