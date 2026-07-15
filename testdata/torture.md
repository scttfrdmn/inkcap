# Decoupled Supersteps for HPC Workloads

A design note on running tightly-coupled simulation codes across ephemeral,
heterogeneous cloud capacity without paying the full price of a persistent
interconnect. This document exists mainly to exercise the typesetter, so it is
deliberately dense with **bold text**, *emphasis*, `inline code`, ~~struck
text~~, and [links to nowhere](https://example.com/very/long/path?with=query).

## Motivation

The classic objection to running MPI codes in the cloud is that you rent the
whole rectangle: nodes, interconnect, and idle time. The rectangle is
*perishable* — every second of an unused reservation is destroyed value that
cannot be recovered. This is not a cloud-specific problem; it is simply more
visible in the cloud because the invoice arrives monthly rather than as a
capital write-down amortised over five years. On-premises clusters hide the
same waste inside a depreciation schedule, which is why utilisation numbers are
so often reported as a virtue rather than as evidence of demand destruction.

### The queueing argument

Consider a facility modelled as M/M/c with c servers and a fixed arrival rate.
As utilisation approaches unity, expected wait time grows without bound. The
cloud, by contrast, approximates M/M/∞: there is no queue, only a price. The
interesting question is not which model is cheaper per core-hour, but which
produces more results per dollar once you account for researcher time spent
waiting.

> The cost of a queue is not paid by the facility. It is paid by the graduate
> student, in units of calendar time, and it never appears on any budget line.
>
> Nobody has ever been promoted for reducing it.

## Architecture

The system decomposes into three planes. Each superstep is checkpointed to
object storage, which decouples the failure domain of the compute from the
failure domain of the job.

| Component | Language | Responsibility | Failure mode |
|-----------|----------|----------------|--------------|
| `spored` | Go | Node-local lifecycle, health, drain | Fail-stop, restarts clean |
| `truffle` | Go | Placement and instance selection across pools, including spot | Degrades to on-demand |
| `cohort` | Go | Named-set reconciler; the only component that holds cluster state | Leader election, WAL replay |
| `lagotto` | Rust | Hot-path data movement between object store and node-local NVMe | Retries with backoff |

Note that the table above has one column whose content is much longer than the
others; the column solver should give it the surplus width rather than wrapping
every column equally.

### Superstep protocol

```go
// Superstep runs one bulk-synchronous iteration and durably commits its
// output before returning. A superstep that does not commit did not happen.
func (c *Cohort) Superstep(ctx context.Context, n int) (Checkpoint, error) {
    members, err := c.reconcile(ctx)
    if err != nil {
        return Checkpoint{}, fmt.Errorf("reconcile superstep %d: %w", n, err)
    }

    results := make(chan Partial, len(members))
    g, gctx := errgroup.WithContext(ctx)
    for _, m := range members {
        m := m
        g.Go(func() error {
            p, err := m.Compute(gctx, n)
            if err != nil {
                return err
            }
            select {
            case results <- p:
                return nil
            case <-gctx.Done():
                return gctx.Err()
            }
        })
    }
    if err := g.Wait(); err != nil {
        return Checkpoint{}, err
    }
    close(results)

    return c.commit(ctx, n, drain(results))
}
```

The line above this comment is intentionally long enough that it must wrap inside the code block, and the wrap should preserve the leading indentation with a visible hanging continuation rather than reflowing the whole thing flush left.

## Operational notes

1. Provision the control plane first. It is small, cheap, and must outlive the
   workers.
2. Bring up workers in waves. The reconciler is level-triggered, so a partial
   wave is not an error condition — it is simply a smaller cluster.
3. Never let a superstep depend on node identity.
   - Node identity is a lie in a spot market.
   - Content-addressed checkpoints make identity irrelevant.
   - If you find yourself pinning ranks to hostnames, stop.
4. Budget is a clock. When the budget is exhausted, the job ends; this is a
   feature, not a failure.

- [x] Reconciler passes chaos tests
- [x] Checkpoint format is content-addressed
- [ ] Spot interruption handler is idempotent
- [ ] Cost receipts emitted per superstep

---

## Appendix: things that must not break

Short paragraph.

Another short paragraph, immediately followed by a fenced block with no
language annotation:

```
$ md2pdf -o out.pdf notes.md
out.pdf
```

#### A fourth-level heading

##### And a fifth

Text under a fifth-level heading, to check that the heading scale does not
collapse into the body size and that the space above it is still legible as a
break in the flow.

This final paragraph is deliberately long so that it has a reasonable chance of
straddling a page boundary, which is exactly the case the orphan and widow
rules are meant to handle. If the paginator is working, you should never see a
single line of this paragraph stranded alone at the foot of a page, nor a
single line carried over by itself to the top of the next one. Two lines is the
minimum on either side of the break. The same rule ought to apply, with
different thresholds, to code blocks and to tables, whose headers should repeat
whenever the body rows spill across a page boundary. If any of that is wrong,
it will be obvious immediately, which is the entire point of writing a test
document that is longer than a single page.
