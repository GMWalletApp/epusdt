package nodes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"gopkg.in/yaml.v3"
)

const benchMCP = "https://openchainbench.com/api/mcp/mcp"
const benchSpecs = "https://raw.githubusercontent.com/ChainBench/OpenChainBench/main/benchmarks/"

var benchNetworks = []struct{ network, slug, protocol string }{
	{"solana", "solana-rpc", "http"}, {"aptos", "aptos-rpc", "http"}, {"sui", "sui-rpc", "grpc"}, {"ton", "gram-rpc", "jsonrpc"},
}

func (r *NodeRegistry) refreshOpenChainBench(ctx context.Context, client *http.Client) {
	round, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for _, chain := range benchNetworks {
		if round.Err() != nil {
			return
		}
		candidates, err := benchCandidates(round, client, benchMCP, benchSpecs, chain.network, chain.slug, chain.protocol)
		if err == nil {
			_, err = r.refreshCandidates(round, "openchainbench", chain.network, candidates, chains.ProbePublicRPC)
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("OpenChainBench %s: %v", chain.network, err)
		}
	}
}

// The stateless MCP service returns rankings; its benchmark specification
// publishes the URLs and access tiers for the same provider slugs.
func benchCandidates(ctx context.Context, c *http.Client, mcp, specBase, network, slug, protocol string) ([]NodeEndpoint, error) {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "get_benchmark", "arguments": map[string]string{"slug": slug}}})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcp, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("MCP HTTP %d", res.StatusCode)
	}
	var reply struct {
		ID     int
		Result struct {
			IsError bool
			Content []struct{ Type, Text string }
		}
	}
	reader := io.LimitReader(res.Body, 1<<20)
	if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		var data []string
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" && len(data) > 0 {
				err = json.Unmarshal([]byte(strings.Join(data, "\n")), &reply)
				if err != nil {
					return nil, err
				}
				if reply.ID == 1 {
					break
				}
				data = nil
			} else if v, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimPrefix(v, " "))
			}
		}
		if err = scanner.Err(); err != nil {
			return nil, err
		}
	} else if err = json.NewDecoder(reader).Decode(&reply); err != nil {
		return nil, err
	}
	if reply.ID != 1 || reply.Result.IsError || len(reply.Result.Content) == 0 || reply.Result.Content[0].Type != "text" {
		return nil, errors.New("invalid benchmark response")
	}
	var ranking struct {
		Slug     string
		Rankings []struct{ Slug string }
	}
	if err = json.Unmarshal([]byte(reply.Result.Content[0].Text), &ranking); err != nil || ranking.Slug != slug || ranking.Rankings == nil {
		return nil, errors.New("missing public benchmark rankings")
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, specBase+slug+".yml", nil)
	if err != nil {
		return nil, err
	}
	spec, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer spec.Body.Close()
	if spec.StatusCode != 200 {
		return nil, fmt.Errorf("benchmark specification HTTP %d", spec.StatusCode)
	}
	const limit = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(spec.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, errors.New("oversized benchmark specification")
	}
	var definition struct {
		Slug      string
		Providers []struct{ Slug, Endpoint, Tier string }
	}
	if err = yaml.Unmarshal(raw, &definition); err != nil || definition.Slug != slug || definition.Providers == nil || len(definition.Providers) > 128 {
		return nil, errors.New("invalid benchmark specification")
	}
	ranked := map[string]bool{}
	for _, v := range ranking.Rankings {
		ranked[v.Slug] = true
	}
	var preferred, others []NodeEndpoint
	seen := map[NodeEndpoint]bool{}
	for _, provider := range definition.Providers {
		rawURL := provider.Endpoint
		if network == "sui" && strings.HasPrefix(rawURL, "grpcs://") {
			rawURL = "https://" + strings.TrimPrefix(rawURL, "grpcs://")
		}
		if provider.Slug == "" || (provider.Tier != "" && provider.Tier != "public") || chains.PublicRPCProtocol(rawURL) != "http" {
			continue
		}
		u, _ := url.Parse(rawURL)
		// The published public tier includes routing parameters and LeoRPC's
		// literal FREE access marker. Never import a private provider key.
		publicQuery := u.RawQuery == "" || (u.Host == "solana.leorpc.com" && u.RawQuery == "api_key=FREE") || (network == "ton" && u.RawQuery == "chainId=toncoin")
		if !publicQuery {
			continue
		}
		e := NodeEndpoint{network, protocol, rawURL}.identity()
		if seen[e] || chains.ValidateNetwork(chains.Network{Name: network, Protocol: protocol, URL: e.URL}) != nil {
			continue
		}
		seen[e] = true
		if ranked[provider.Slug] {
			preferred = append(preferred, e)
		} else {
			others = append(others, e)
		}
	}
	return slices.Concat(preferred, others), nil
}
