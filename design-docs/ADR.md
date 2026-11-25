# Architecture decisions

Each entry is a decision in force, stated as its title; the linked record holds the rule in full, why it holds, what
lost, and what would reopen it.

- Before changing code under an area below, read the records listed for it.
- A task that conflicts with a listed decision stops and asks; it does not change the decision or work around it.
- Writing, changing, or removing a record follows the `the-blueprint:adr` skill.

## internal/config

- [1.2 Default to the read group, and to trail-leaving on own changes only](/design-docs/adr/1.2-safe-by-default-posture.md)

## internal/llmxml

- [1.3 Return llmxml from every tool that a model reads](/design-docs/adr/1.3-llmxml-output.md)

## internal/registry

- [1.1 Independent, self-sufficient capability groups](/design-docs/adr/1.1-independent-capability-groups.md)

## plugins/gerrit-review-notifications

- [2.1 Deliver review notifications through a Claude Code plugin](/design-docs/adr/2.1-notification-delivery.md)
- [2.2 Session-scoped explicit subscriptions over account-wide watching](/design-docs/adr/2.2-session-scoped-subscriptions.md)
