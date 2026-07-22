// Package sdk holds the scenario that drives a site through the generated .NET
// SDK, with nothing simulated on either side.
//
// The builder builds two machines from one blueprint, both run as real
// processes over a real site journal, and a .NET consumer drives them through
// the SDK. The SDK is the subject rather than the transport: the registration
// contract is asynchronous, so what is checked is that a generated client can
// take a proposal's identity from a 202 and follow it to a decision.
//
// The two processes coordinate through a file marker rather than a delay. The
// .NET test asserts the pending behavior while the second machine is absent,
// writes the marker, and only then does this scenario start that machine. The
// marker name is shared with sdk-dotnet/tests/Opdl.Sdk.E2E/RegistrationTests.cs.
//
// The shared machinery is internal/harness; see its documentation for the
// scratch layout, the machine budget, and the failure diagnostics.
package sdk
