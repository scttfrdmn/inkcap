# Decoupled Supersteps

A design note on ephemeral capacity[^rect], written to exercise every feature
the typesetter claims to have. Raw HTML like <kbd>Ctrl</kbd> should keep its
text and lose its tags,<br>with a warning on stderr.

[^rect]: The *perishable rectangle*: every second of an unused reservation is
destroyed value. This footnote is deliberately long enough to occupy two lines
in the footnote area, so that the space reservation can be seen to work.

## Motivation

The classic objection is that you rent the whole rectangle. The cloud
approximates M/M/∞[^queue] — there is no queue, only a price.

[^queue]: Kleinrock, *Queueing Systems*, vol. 1.

<div class="callout">
  This is an HTML block. Its tags are stripped and its text is kept.
</div>

## Architecture

### The reconciler

Level-triggered, not edge-triggered. A partial wave is a smaller cluster, not
an error[^lvl].

[^lvl]: This is the single most load-bearing idea in the design.

| Component | Notes |
|---|---|
| `cohort` | Named-set reconciler. This cell is deliberately enormous so that the row cannot possibly fit on a page and the intra-row splitter has to break it at a line boundary. Reconciliation is level-triggered: the desired state is declared, the observed state is polled, and the difference is applied. There is no event log, no ordering requirement, and no way for a dropped message to corrupt the cluster, because there are no messages — only observations. A node that vanishes is simply absent from the next observation, and the next reconcile brings the cluster back to the declared shape. A node that reappears is likewise just present again. This makes the whole system trivially restartable: the reconciler holds no durable state of its own beyond the declaration, and the declaration lives in object storage. It also means the reconciler can be killed at any point without consequence, which is the property that makes it safe to run on spot capacity. The cost of this design is polling latency; the benefit is that there is no failure mode more complicated than "try again in five seconds", which is a trade almost every distributed system should make and almost none do. Repeat this paragraph in your head until it fits on a page, which it will not. |
| `truffle` | Placement. |

## Operations

1. Provision the control plane first.
2. Bring up workers in waves.
3. Never depend on node identity.

### Budget

Budget is a clock. When it is exhausted, the job ends.

## Appendix

Short closing paragraph.
