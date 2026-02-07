# Flight Search Assistant (AGK Template)

Config-driven flight search workflow using the Amadeus API.

## What it does
1) Extracts search constraints
2) Calls the `flight_search` tool (Amadeus API)
3) Summarizes results

## How it works
- Workflow is defined in `workflow.toml`.
- Prompts live in `prompts/` and are injected into the workflow config at runtime.
- The tool is implemented in `tools/flight_search.go` and registered as an internal tool.

## Run
```bash
go run .
```

## Environment variables
- INPUT_TEXT (optional input; defaults to a sample query)
- AMADEUS_CLIENT_ID (Amadeus API key)
- AMADEUS_CLIENT_SECRET (Amadeus API secret)
- AMADEUS_BASE_URL (optional; default https://test.api.amadeus.com)

## Notes
- The tool requires valid Amadeus credentials and will error if they are missing.
- The search step is the only one with tools enabled.
