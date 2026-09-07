package cli

import (
	"fmt"
	"io"

	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/toolcache"
)

type discoveredToolCatalog struct {
	summaries []toolSummary
	details   map[string]toolDescription
}

func toolMetadataIdentity(cfg *config.Config, server config.Server, cwd string) string {
	workspace := cwd
	if cfg.Root != nil {
		workspace = resolveRoot(*cfg.Root)
	}
	return fingerprint(map[string]any{
		"v":         1,
		"server":    server.Name,
		"workspace": workspace,
		"config":    configFingerprint(cfg, cwd),
		"execution": executionFingerprint(server, cfg.Root, cwd),
	})
}

func replaceToolMetadata(cfg *config.Config, server config.Server, cwd string, catalog discoveredToolCatalog) error {
	store, err := toolcache.New()
	if err != nil {
		return err
	}
	tools := make([]toolcache.Tool, 0, len(catalog.summaries))
	for _, summary := range catalog.summaries {
		entry := toolcache.Tool{Name: singleLine(summary.Name), Title: singleLine(summary.Title), Description: readableText(summary.Description)}
		if detail, ok := catalog.details[summary.Name]; ok {
			entry.Detailed = true
			entry.InputSchema = detail.InputSchema
			entry.OutputSchema = detail.OutputSchema
		}
		tools = append(tools, entry)
	}
	return store.Replace(toolMetadataIdentity(cfg, server, cwd), toolcache.Catalog{Server: singleLine(server.Name), Complete: true, Tools: tools})
}

func replaceToolDetailMetadata(cfg *config.Config, server config.Server, cwd string, detail toolDescription) error {
	store, err := toolcache.New()
	if err != nil {
		return err
	}
	entry := toolcache.Tool{
		Name:         singleLine(detail.Name),
		Title:        singleLine(detail.Title),
		Description:  readableText(detail.Description),
		InputSchema:  detail.InputSchema,
		OutputSchema: detail.OutputSchema,
		Detailed:     true,
	}
	return store.MergeExact(toolMetadataIdentity(cfg, server, cwd), singleLine(server.Name), entry)
}

func mergeProjectedToolMetadata(cfg *config.Config, server config.Server, cwd string, detail toolDescription) error {
	store, err := toolcache.New()
	if err != nil {
		return err
	}
	return store.Merge(toolMetadataIdentity(cfg, server, cwd), singleLine(server.Name), toolcache.Tool{
		Name:        singleLine(detail.Name),
		InputSchema: detail.InputSchema,
		Detailed:    true,
	})
}

func redactedToolMetadata(detail toolDescription, redactor *redactor) (toolDescription, error) {
	input, err := redactedSchema(detail.InputSchema, redactor)
	if err != nil {
		return toolDescription{}, err
	}
	output, err := redactedSchema(detail.OutputSchema, redactor)
	if err != nil {
		return toolDescription{}, err
	}
	return toolDescription{
		Name:         redactor.Redact(detail.Name),
		Title:        redactor.Redact(detail.Title),
		Description:  redactor.Redact(detail.Description),
		InputSchema:  input,
		OutputSchema: output,
	}, nil
}

func cachedFocusedHelp(cfg *config.Config, server config.Server, cwd string, req request) (helpText, bool) {
	store, err := toolcache.New()
	if err != nil {
		return "", false
	}
	catalog, err := store.Load(toolMetadataIdentity(cfg, server, cwd))
	if err != nil {
		return "", false
	}
	switch req.help {
	case serverHelp:
		if !catalog.Complete {
			return "", false
		}
		tools := make([]toolSummary, 0, len(catalog.Tools))
		for _, tool := range catalog.Tools {
			tools = append(tools, toolSummary{Name: tool.Name, Title: tool.Title, Description: tool.Description})
		}
		return helpText(renderServerHelp(server.Name, tools)), true
	case toolHelp:
		for _, tool := range catalog.Tools {
			if tool.Name == req.tool && tool.Detailed {
				return helpText(renderToolHelp(server.Name, toolDescription{Name: tool.Name, Title: tool.Title, Description: tool.Description, InputSchema: tool.InputSchema, OutputSchema: tool.OutputSchema})), true
			}
		}
	}
	return "", false
}

func warnToolMetadataCache(errOut io.Writer) {
	fmt.Fprintln(errOut, "wirecmd warning: MCP tool metadata could not be cached; offline focused help may be unavailable")
}
