package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/bjthomas07/push-dispatch/scheduler"
)

func TestHelpAndInvalidCommandsDoNotNeedCredentials(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"help"}, {"tick", "--help"}} {
		var out bytes.Buffer
		if err := run(context.Background(), args, strings.NewReader(""), &out); err != nil || out.Len() == 0 {
			t.Fatalf("help %v: %v", args, err)
		}
	}
	if err := run(context.Background(), []string{"unknown"}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("accepted unknown command")
	}
}

func TestCloudRunPartitionCoversEveryShardExactlyOnce(t *testing.T) {
	for tasks := 1; tasks <= scheduler.ShardCount; tasks++ {
		seen := make(map[int]int)
		for task := 0; task < tasks; task++ {
			first, last, stride, err := shardRange(-1, strconv.Itoa(task), strconv.Itoa(tasks))
			if err != nil {
				t.Fatal(err)
			}
			for n := first; n <= last; n += stride {
				seen[n]++
			}
		}
		if len(seen) != scheduler.ShardCount {
			t.Fatalf("tasks=%d skipped shards", tasks)
		}
		for shard, count := range seen {
			if count != 1 {
				t.Fatalf("shard %d assigned %d times", shard, count)
			}
		}
	}
}

func TestReadJSONRejectsTrailingObjectsAndUnknownKeys(t *testing.T) {
	for _, s := range []string{`{"bad":1}`, `{} {}`, `{"id":`} {
		var v struct {
			ID string `json:"id"`
		}
		if readJSON(strings.NewReader(s), &v) == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
