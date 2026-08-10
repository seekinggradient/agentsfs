---
description: Session — added safe Mermaid and flowchart rendering to the Hub backlog.
---

## Learned / decided
- The Hub should render fenced Mermaid diagrams, with flowcharts as the first required diagram type, rather than exposing the diagram source as an ordinary code block.
- Rendering should cover both file and share-link views, remain sandboxed, preserve source-oriented views, and degrade to readable source on failure.

## Open
- Choose the Mermaid runtime, version-pinning policy, and final sandbox boundary during implementation.

## Written directly
- Added [[backlog/INDEX#^hub-mermaid-rendering]] under Later.
