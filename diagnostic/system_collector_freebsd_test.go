package diagnostic

import (
    "context"
    "errors"
    "strings"
    "testing"
    "time"
    "strconv"
)

// TestNewSystemCollectorImpl tests that NewSystemCollectorImpl correctly assigns the version.
func TestNewSystemCollectorImpl(t *testing.T) {
    version := "v1.0-test"
    collector := NewSystemCollectorImpl(version)
    if collector.version != version {
        t.Errorf("expected version %s, got %s", version, collector.version)
    }
}
// TestNewSystemCollectorImplWithEmptyVersion verifies that creating a SystemCollectorImpl with an empty version string works properly.
func TestNewSystemCollectorImplWithEmptyVersion(t *testing.T) {
    collector := NewSystemCollectorImpl("")
    if collector.version != "" {
        t.Errorf("expected empty version, got %s", collector.version)
    }
}

// TestCollectWithCanceledContext tests the Collect method using a canceled context so that
// the underlying exec.CommandContext calls fail. It asserts that SystemInformation is still returned
// and that the error object contains at least one non-nil error field.
func TestCollectWithCanceledContext(t *testing.T) {
    // Create a context that is immediately canceled.
    ctx, cancel := context.WithCancel(context.Background())
    cancel()

    collector := NewSystemCollectorImpl("v-test")
    info, err := collector.Collect(ctx)
    if info == nil {
        t.Error("expected non-nil system information even on error")
    }

    sysErr, ok := err.(SystemInformationGeneralError)
    if !ok {
        t.Error("expected error to be of type SystemInformationGeneralError")
    }

    // Check that at least one of the error fields is non-nil (since the context is canceled)
    if sysErr.MemoryInformationError == nil && sysErr.FileDescriptorsInformationError == nil &&
        sysErr.DiskVolumeInformationError == nil && sysErr.OperatingSystemInformationError == nil {
        t.Error("expected at least one error field to be non-nil due to canceled context")
    }
}

// TestCollectMemoryInformation tests the collectMemoryInformation function using a context with a very short timeout.
func TestCollectMemoryInformation(t *testing.T) {
    // Use a context with an extremely short timeout to force a deadline error.
    ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
    defer cancel()
    memInfo, raw, err := collectMemoryInformation(ctx)

    if err == nil {
        // If the command did not error, we check that MemoryMaximum is non-zero.
        if memInfo.MemoryMaximum == 0 {
            t.Error("expected non-zero MemoryMaximum")
        }
    } else {
        // On error, raw output should be non-empty or the error message should mention a deadline exceeded error.
        if raw == "" && !strings.Contains(err.Error(), "deadline") && !errors.Is(err, context.DeadlineExceeded) {
            t.Error("expected raw output to be non-empty or error to mention deadline exceeded")
        }
    }
}

// TestCollectFileDescriptorInformation tests the collectFileDescriptorInformation function using a canceled context.
func TestCollectFileDescriptorInformation(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    cancel()

    fdInfo, raw, err := collectFileDescriptorInformation(ctx)

    if err == nil {
        // If no error occurred, check that FileDescriptorMaximum is non-zero.
        if fdInfo.FileDescriptorMaximum == 0 {
            t.Error("expected non-zero FileDescriptorMaximum")
        }
    } else {
        // On error, raw output should be non-empty or the error should indicate a canceled context.
        if raw == "" && !strings.Contains(err.Error(), "canceled") {
            t.Error("expected raw output to be non-empty or error to mention canceled context")
        }
    }
}
// TestCollectOSInformation tests the collectOSInformationUnix function using a canceled context.
func TestCollectOSInformation(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    cancel()

    osInfo, raw, err := collectOSInformationUnix(ctx)
    if err == nil {
        // If no error occurred, check that essential OS fields are non-empty.
        if osInfo.OsSystem == "" || osInfo.Architecture == "" || osInfo.OsVersion == "" || osInfo.OsRelease == "" || osInfo.Name == "" {
            t.Error("expected non-empty OS information fields")
        }
    } else {
        // On error, verify that raw output is non-empty or that the error message indicates a cancellation.
        if raw == "" && !strings.Contains(err.Error(), "canceled") && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "deadline") {
            t.Error("expected raw output or an error indicating that the context was canceled or deadline exceeded")
        }
    }
}

// TestCollectDiskVolumeInformation tests the collectDiskVolumeInformationUnix function using a canceled context.
func TestCollectDiskVolumeInformation(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    cancel()

    disks, raw, err := collectDiskVolumeInformationUnix(ctx)
    if err == nil {
        // If no error occurred, disks should be a non-empty slice.
        if len(disks) == 0 {
            t.Error("expected non-empty disk volume information")
        }
    } else {
        // On error, verify that the raw output is non-empty or that the error message mentions cancellation or deadline issues.
        if raw == "" && !strings.Contains(err.Error(), "canceled") && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "deadline") {
            t.Error("expected raw output or an error indicating that the context was canceled or deadline exceeded")
        }
    }
}
// TestCollectSuccess tests a normal Collect call to retrieve system information and verify basic attributes.
func TestCollectSuccess(t *testing.T) {
    // Use a normal context so that sysctl commands are allowed to run.
    ctx := context.Background()
    collector := NewSystemCollectorImpl("v-test-success")
    info, err := collector.Collect(ctx)
    if info == nil {
        t.Fatal("expected non-nil system information")
    }
    sysErr, ok := err.(SystemInformationGeneralError)
    if !ok {
        t.Fatal("expected error to be of type SystemInformationGeneralError")
    }
    // Verify that the Cloudflare version equals the one provided upon collector creation.
    if info.CloudflaredVersion != "v-test-success" {
        t.Errorf("expected cloudflared version 'v-test-success', got '%s'", info.CloudflaredVersion)
    }
    // Check that GoVersion is populated.
    if info.GoVersion == "" {
        t.Error("expected non-empty GoVersion")
    }
    // If the MemoryInformation collection did not error, MemoryMaximum should be non-zero.
    if sysErr.MemoryInformationError == nil && info.MemoryMaximum == 0 {
        t.Error("expected non-zero MemoryMaximum in successful MemoryInformation collection")
    }
}
// TestSystemInformationGeneralError_Error verifies that the Error() method of SystemInformationGeneralError
// returns an empty string when no errors are present and contains details when errors exist.
func TestSystemInformationGeneralError_Error(t *testing.T) {
    // When no error fields are set, we expect an empty error string.
    var errAgg SystemInformationGeneralError
    if errAgg.Error() != "" {
        t.Errorf("expected empty error string for no errors, got: %s", errAgg.Error())
    }

    // Now set one or more error fields.
    dummy := errors.New("dummy error")
    errAgg.MemoryInformationError = SystemInformationError{Err: dummy, RawInfo: "raw memory info"}
    errAgg.FileDescriptorsInformationError = SystemInformationError{Err: dummy, RawInfo: "raw fd info"}

    out := errAgg.Error()
    if !strings.Contains(out, "dummy error") {
        t.Errorf("expected error string to contain 'dummy error', got: %s", out)
    }
}

// TestParseMemoryInformationFromKVValid verifies that ParseMemoryInformationFromKV correctly parses valid output.
func TestParseMemoryInformationFromKVValid(t *testing.T) {
    // Create an output string as expected from sysctl.
    // For example, assume the actual sysctl returns values in bytes so that our mapper divides by 1024.
    output := "hw.memsize: 8589934592\nhw.memsize_usable: 4294967296\n"

    mapper := func(field string) (uint64, error) {
        const kiloBytes = 1024
        value, err := strconv.ParseUint(field, 10, 64)
        return value / kiloBytes, err
    }

    mi, err := ParseMemoryInformationFromKV(output, "hw.memsize", "hw.memsize_usable", mapper)
    if err != nil {
        t.Fatalf("expected no error, got: %v", err)
    }
    // 8589934592/1024 = 8388608 and 4294967296/1024 = 4194304
    if mi.MemoryMaximum != 8388608 || mi.MemoryCurrent != 4194304 {
        t.Errorf("unexpected values: got MemoryMaximum=%d, MemoryCurrent=%d", mi.MemoryMaximum, mi.MemoryCurrent)
    }
}

// TestParseMemoryInformationFromKVInvalid verifies that ParseMemoryInformationFromKV returns an error when output is invalid.
func TestParseMemoryInformationFromKVInvalid(t *testing.T) {
    // Provide an invalid numeric value for hw.memsize.
    output := "hw.memsize: notanumber\nhw.memsize_usable: 4294967296\n"

    mapper := func(field string) (uint64, error) {
        return strconv.ParseUint(field, 10, 64)
    }

    _, err := ParseMemoryInformationFromKV(output, "hw.memsize", "hw.memsize_usable", mapper)
    if err == nil {
        t.Fatal("expected an error for invalid memsize value")
    }
}

// TestParseFileDescriptorInformationFromKVValid verifies that ParseFileDescriptorInformationFromKV correctly parses valid output.
func TestParseFileDescriptorInformationFromKVValid(t *testing.T) {
    // Construct a valid output from sysctl.
    output := "kern.maxfiles: 10240\nkern.num_files: 512\n"

    fdInfo, err := ParseFileDescriptorInformationFromKV(output, "kern.maxfiles", "kern.num_files")
    if err != nil {
        t.Fatalf("expected no error, got: %v", err)
    }
    if fdInfo.FileDescriptorMaximum != 10240 || fdInfo.FileDescriptorCurrent != 512 {
        t.Errorf("unexpected values: got FileDescriptorMaximum=%d, FileDescriptorCurrent=%d", fdInfo.FileDescriptorMaximum, fdInfo.FileDescriptorCurrent)
    }
}

// TestParseFileDescriptorInformationFromKVInvalid verifies that ParseFileDescriptorInformationFromKV returns an error when output is invalid.
func TestParseFileDescriptorInformationFromKVInvalid(t *testing.T) {
    // Provide invalid output where the maxfiles number is not numeric.
    output := "kern.maxfiles: abc\nkern.num_files: 512\n"

    _, err := ParseFileDescriptorInformationFromKV(output, "kern.maxfiles", "kern.num_files")
    if err == nil {
        t.Fatal("expected an error for invalid kern.maxfiles value")
    }
}
// End of tests.
    // TestParseMemoryInformationFromKVMissingKey verifies that ParseMemoryInformationFromKV returns an error when an expected key is missing.
    func TestParseMemoryInformationFromKVMissingKey(t *testing.T) {
        // Provide output missing the "hw.memsize" key.
        output := "hw.memsize_usable: 4294967296\n"
        mapper := func(field string) (uint64, error) {
            return strconv.ParseUint(strings.TrimSpace(field), 10, 64)
        }
        _, err := ParseMemoryInformationFromKV(output, "hw.memsize", "hw.memsize_usable", mapper)
        if err == nil {
            t.Fatal("expected an error when the hw.memsize key is missing")
        }
    }

    // TestParseFileDescriptorInformationFromKVMissingKey verifies that ParseFileDescriptorInformationFromKV returns an error when an expected key is missing.
    func TestParseFileDescriptorInformationFromKVMissingKey(t *testing.T) {
        // Provide output missing the "kern.num_files" key.
        output := "kern.maxfiles: 10240\n"
        _, err := ParseFileDescriptorInformationFromKV(output, "kern.maxfiles", "kern.num_files")
        if err == nil {
            t.Fatal("expected an error when the kern.num_files key is missing")
        }
    }

    // TestParseMemoryInformationWithExtraSpaces verifies that ParseMemoryInformationFromKV correctly parses values with extra spaces.
    func TestParseMemoryInformationWithExtraSpaces(t *testing.T) {
        // Provide an output with extra spaces in the values.
        output := "hw.memsize:    8589934592 \n hw.memsize_usable: 4294967296 \n"
        mapper := func(field string) (uint64, error) {
            const kiloBytes = 1024
            value, err := strconv.ParseUint(strings.TrimSpace(field), 10, 64)
            return value / kiloBytes, err
        }
        mi, err := ParseMemoryInformationFromKV(output, "hw.memsize", "hw.memsize_usable", mapper)
        if err != nil {
            t.Fatalf("expected no error, got: %v", err)
        }
        // Expected: 8589934592/1024 = 8388608 and 4294967296/1024 = 4194304.
        if mi.MemoryMaximum != 8388608 || mi.MemoryCurrent != 4194304 {
            t.Errorf("unexpected values: got MemoryMaximum=%d, MemoryCurrent=%d", mi.MemoryMaximum, mi.MemoryCurrent)
        }
    }
// TestParseMemoryInformationFromKVEmptyOutput tests that ParseMemoryInformationFromKV returns an error when given empty output.
func TestParseMemoryInformationFromKVEmptyOutput(t *testing.T) {
    mapper := func(field string) (uint64, error) {
        return strconv.ParseUint(strings.TrimSpace(field), 10, 64)
    }
    _, err := ParseMemoryInformationFromKV("", "hw.memsize", "hw.memsize_usable", mapper)
    if err == nil {
        t.Fatal("expected an error for empty output")
    }
}

// TestParseFileDescriptorInformationFromKVEmptyOutput tests that ParseFileDescriptorInformationFromKV returns an error when given empty output.
func TestParseFileDescriptorInformationFromKVEmptyOutput(t *testing.T) {
    _, err := ParseFileDescriptorInformationFromKV("", "kern.maxfiles", "kern.num_files")
    if err == nil {
        t.Fatal("expected an error for empty output")
    }
}
// TestParseMemoryInformationFromKVWithExtraData verifies that ParseMemoryInformationFromKV can parse outputs that include extra irrelevant lines.
func TestParseMemoryInformationFromKVWithExtraData(t *testing.T) {
    output := "hw.memsize: 8589934592\nirrelevant.line: abc\nhw.memsize_usable: 4294967296\nmore.data: ignored\n"
    mapper := func(field string) (uint64, error) {
        const kiloBytes = 1024
        value, err := strconv.ParseUint(strings.TrimSpace(field), 10, 64)
        return value / kiloBytes, err
    }
    mi, err := ParseMemoryInformationFromKV(output, "hw.memsize", "hw.memsize_usable", mapper)
    if err != nil {
        t.Fatalf("expected no error, got: %v", err)
    }
    if mi.MemoryMaximum != 8388608 || mi.MemoryCurrent != 4194304 {
        t.Errorf("unexpected values: got MemoryMaximum=%d, MemoryCurrent=%d", mi.MemoryMaximum, mi.MemoryCurrent)
    }
}

// TestParseFileDescriptorInformationFromKVWithExtraData verifies that ParseFileDescriptorInformationFromKV can parse outputs that include extra irrelevant lines.
func TestParseFileDescriptorInformationFromKVWithExtraData(t *testing.T) {
    output := "kern.maxfiles: 10240\nextradata: ignore\nkern.num_files: 1024\nmoreinfo: none\n"
    fdInfo, err := ParseFileDescriptorInformationFromKV(output, "kern.maxfiles", "kern.num_files")
    if err != nil {
        t.Fatalf("expected no error, got: %v", err)
    }
    if fdInfo.FileDescriptorMaximum != 10240 || fdInfo.FileDescriptorCurrent != 1024 {
        t.Errorf("unexpected values: got FileDescriptorMaximum=%d, FileDescriptorCurrent=%d", fdInfo.FileDescriptorMaximum, fdInfo.FileDescriptorCurrent)
    }
}
// TestParseMemoryInformationFromKVKeysSwapped verifies that ParseMemoryInformationFromKV correctly parses output even when the key lines are swapped.
func TestParseMemoryInformationFromKVKeysSwapped(t *testing.T) {
    // Provide sysctl output with keys in swapped order.
    output := "hw.memsize_usable: 4294967296\nhw.memsize: 8589934592\n"

    mapper := func(field string) (uint64, error) {
        const kiloBytes = 1024
        value, err := strconv.ParseUint(strings.TrimSpace(field), 10, 64)
        return value / kiloBytes, err
    }

    mi, err := ParseMemoryInformationFromKV(output, "hw.memsize", "hw.memsize_usable", mapper)
    if err != nil {
        t.Fatalf("expected no error, got: %v", err)
    }

    // Expected values: MemoryMaximum = 8589934592/1024 = 8388608, MemoryCurrent = 4294967296/1024 = 4194304.
    if mi.MemoryMaximum != 8388608 || mi.MemoryCurrent != 4194304 {
        t.Errorf("unexpected values: got MemoryMaximum=%d, MemoryCurrent=%d", mi.MemoryMaximum, mi.MemoryCurrent)
    }
}

// TestParseFileDescriptorInformationFromKVKeysSwapped verifies that ParseFileDescriptorInformationFromKV correctly parses output with swapped key order.
func TestParseFileDescriptorInformationFromKVKeysSwapped(t *testing.T) {
    // Provide sysctl output with keys in swapped order.
    output := "kern.num_files: 512\nkern.maxfiles: 10240\n"

    fdInfo, err := ParseFileDescriptorInformationFromKV(output, "kern.maxfiles", "kern.num_files")
    if err != nil {
        t.Fatalf("expected no error, got: %v", err)
    }

    if fdInfo.FileDescriptorMaximum != 10240 || fdInfo.FileDescriptorCurrent != 512 {
        t.Errorf("unexpected values: got FileDescriptorMaximum=%d, FileDescriptorCurrent=%d", fdInfo.FileDescriptorMaximum, fdInfo.FileDescriptorCurrent)
    }
}
// TestParseMemoryInformationFromKVWithLeadingZeroes verifies that ParseMemoryInformationFromKV correctly handles numeric values with leading zeroes.
func TestParseMemoryInformationFromKVWithLeadingZeroes(t *testing.T) {
    // Output with leading zeroes for memsize values. Expect that ParseMemoryInformationFromKV parses these correctly.
    output := "hw.memsize: 08589934592\nhw.memsize_usable: 04294967296\n"
    mapper := func(field string) (uint64, error) {
        const kiloBytes = 1024
        // Remove any leading zeros before conversion.
        field = strings.TrimLeft(field, "0")
        if field == "" {
            field = "0"
        }
        value, err := strconv.ParseUint(field, 10, 64)
        return value / kiloBytes, err
    }
    mi, err := ParseMemoryInformationFromKV(output, "hw.memsize", "hw.memsize_usable", mapper)
    if err != nil {
        t.Fatalf("expected no error, got: %v", err)
    }
    // 8589934592/1024 = 8388608 and 4294967296/1024 = 4194304.
    if mi.MemoryMaximum != 8388608 || mi.MemoryCurrent != 4194304 {
        t.Errorf("unexpected values: got MemoryMaximum=%d, MemoryCurrent=%d", mi.MemoryMaximum, mi.MemoryCurrent)
    }
}

// TestParseFileDescriptorInformationFromKVWithLeadingZeroes verifies that ParseFileDescriptorInformationFromKV correctly handles numeric values with leading zeroes.
func TestParseFileDescriptorInformationFromKVWithLeadingZeroes(t *testing.T) {
    // Provide sysctl output with leading zeroes.
    output := "kern.maxfiles: 010240\nkern.num_files: 000512\n"
    fdInfo, err := ParseFileDescriptorInformationFromKV(output, "kern.maxfiles", "kern.num_files")
    if err != nil {
        t.Fatalf("expected no error, got: %v", err)
    }
    if fdInfo.FileDescriptorMaximum != 10240 || fdInfo.FileDescriptorCurrent != 512 {
        t.Errorf("unexpected values: got FileDescriptorMaximum=%d, FileDescriptorCurrent=%d", fdInfo.FileDescriptorMaximum, fdInfo.FileDescriptorCurrent)
    }
}

// TestSystemInformationGeneralError_SingleError verifies that SystemInformationGeneralError's Error method returns
// a proper string when only one of its error fields is set.
func TestSystemInformationGeneralError_SingleError(t *testing.T) {
    dummy := errors.New("single error")
    errAgg := SystemInformationGeneralError{
        MemoryInformationError: SystemInformationError{Err: dummy, RawInfo: "raw mem"},
    }
    out := errAgg.Error()
    if !strings.Contains(out, "single error") {
        t.Errorf("expected error string to contain 'single error', got: %s", out)
    }
}
// TestCollectNoErrors tests that when all underlying sysctl calls succeed,
// collector.Collect returns an empty aggregated error (i.e. no error string)
// and correctly sets important fields such as CloudflaredVersion.
func TestCollectNoErrors(t *testing.T) {
    ctx := context.Background()
    collector := NewSystemCollectorImpl("v-no-error")
    info, err := collector.Collect(ctx)
    if info == nil {
        t.Fatal("expected non-nil system information")
    }
    // When no sub error occurred, the aggregated error should report an empty error string.
    if err != nil && err.Error() != "" {
        t.Errorf("expected empty error string, got: %s", err.Error())
    }
    // Also verifying that key fields are properly set.
    if info.CloudflaredVersion != "v-no-error" {
        t.Errorf("expected cloudflared version 'v-no-error', got: %s", info.CloudflaredVersion)
    }
    if info.GoVersion == "" {
        t.Error("expected non-empty GoVersion")
    }
}

// TestParseMemoryInformationFromKVMapperError tests that ParseMemoryInformationFromKV
// returns an error when the mapper function passed in fails.
func TestParseMemoryInformationFromKVMapperError(t *testing.T) {
    output := "hw.memsize: 8589934592\nhw.memsize_usable: 4294967296\n"
    // Here, the mapper always returns an error.
    mapper := func(field string) (uint64, error) {
        return 0, errors.New("mapper error")
    }
    _, err := ParseMemoryInformationFromKV(output, "hw.memsize", "hw.memsize_usable", mapper)
    if err == nil {
        t.Fatal("expected mapper error")
    }
    if !strings.Contains(err.Error(), "mapper error") {
        t.Errorf("expected error to mention 'mapper error', got: %s", err.Error())
    }
}