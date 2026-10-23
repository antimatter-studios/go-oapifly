package oapifly

import (
	"path/filepath"
	"sync"
	"testing"
)

// Several first requests at once each build the document. They must get the same one, and
// the race detector must find nothing (#19); CI runs the tests with -race.
func TestGenerateIsSafeWhenFirstCalledConcurrently(t *testing.T) {
	dir := t.TempDir()
	writeIn(t, filepath.Join(dir, "types"), "types.go", paritySource)
	writeIn(t, filepath.Join(dir, "handlers"), "handlers.go", parityHandler)
	g := New(Config{
		ScanPatterns: []string{filepath.Join(dir, "handlers", "*.go")},
		TypeDirs:     []string{filepath.Join(dir, "types")},
	})
	g.Register(Endpoint{Method: "GET", Path: "/registered", Responses: []EndpointResponse{{Status: 204}}})

	const callers = 16
	specs := make([]map[string]interface{}, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 3 {
			case 0:
				specs[i] = g.Generate()
			case 1:
				if _, err := g.JSON(); err != nil {
					t.Error(err)
				}
				specs[i] = g.Generate()
			case 2:
				if _, err := g.YAML(); err != nil {
					t.Error(err)
				}
				specs[i] = g.Generate()
			}
		}(i)
	}
	wg.Wait()

	for i := 1; i < callers; i++ {
		if asJSON(t, specs[i]) != asJSON(t, specs[0]) {
			t.Fatalf("caller %d got a different document", i)
		}
	}
	paths := specs[0]["paths"].(map[string]map[string]PathItem)
	if _, ok := paths["/parity"]; !ok {
		t.Fatalf("the annotated route is described: %v", paths)
	}
	if _, ok := paths["/registered"]; !ok {
		t.Fatalf("the registered route is described: %v", paths)
	}
}

// Registering while documents are being built is safe, and the registration is in the next
// document.
func TestRegisterWhileGenerating(t *testing.T) {
	g := New(Config{})
	g.Register(Endpoint{Method: "GET", Path: "/first", Responses: []EndpointResponse{{Status: 204}}})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); g.Generate() }()
		go func(i int) {
			defer wg.Done()
			g.Register(Endpoint{Method: "GET", Path: "/late/" + string(rune('a'+i)), Responses: []EndpointResponse{{Status: 204}}})
		}(i)
	}
	wg.Wait()
	if paths := g.Generate()["paths"].(map[string]map[string]PathItem); len(paths) != 9 {
		t.Fatalf("every registration is in the document: %d paths", len(paths))
	}
}
