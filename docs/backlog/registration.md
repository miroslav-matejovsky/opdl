# Registration

Deferred product behavior for the event-sourced registration domain.

## Add explicit key release before supporting re-registration

**Effort:** medium. **Value:** medium.

The first proposal in journal order permanently claims its unit key, even when
an expected machine rejects it. This is deterministic and acceptable for the
current POC, but it means a rejected key cannot be registered again.

When removal or re-registration enters scope, add an explicit versioned release
event. Define who may state it, which prior states permit it, and whether the
released key can be reclaimed. Do not implement release as projection cleanup or
as deletion from local state.
