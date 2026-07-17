# F017: Graceful shutdown lacks a portable black-box test

Category: Testing  
Severity: Low  
Status: Risk accepted  
Source: Stage 4

## Finding

The Windows scenario harness cannot portably deliver a graceful process signal.
It force-stops child processes, so graceful ordering is not covered end to end.

## Resolution

In-process app tests cover handler drain, stopping publication, projector stop,
and transport close. Black-box restart tests cover recovery from the harder
force-stop case.

## Recommendation

Keep the current coverage. Add a Linux process-signal scenario when CI provides
a stable Linux execution lane. Do not add platform-specific signal emulation to
the application.
