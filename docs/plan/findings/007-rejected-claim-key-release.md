# F007: Rejected claims do not release a key

Category: Domain policy  
Severity: High  
Status: Open  
Source: Stage 1

## Finding

The first proposal in journal order permanently claims its unit key even when
an expected machine rejects it. The key cannot be registered again.

## Resolution

No implicit cleanup was added. Implicit release would make replay depend on an
unstated rule and could allow two histories to disagree.

## Recommendation

Add an explicit versioned release or removal event before re-registration is a
required feature. Define who may state it, what prior state it requires, and
whether a released key can be reclaimed. Until then, accept the POC limitation
and expose the rejected proposal to operators.
