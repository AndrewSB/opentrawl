package cli

// acquisitionSupported is a variable so the platform-independent CLI tests
// can continue to exercise sync orchestration on every CI runner. Production
// starts from the build-tagged platform fact.
var acquisitionSupported = platformAcquisitionSupported
