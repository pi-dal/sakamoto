import Foundation

/// Admission for one OS-owned provider launch. The shared native journal owns
/// the retry budget; this adapter never resets it or schedules another launch.
public enum TunnelRecoveryAdmission {
    public static func begin(
        manual: Bool,
        isActive: () -> Bool,
        begin: (Bool) throws -> String,
        status: () throws -> String
    ) async throws -> String {
        guard isActive(), !Task.isCancelled else { throw CancellationError() }
        do { return try begin(manual) }
        catch {
            guard !manual else { throw error }
            let initialError = error
            guard isActive(), !Task.isCancelled else { throw CancellationError() }
            // Read the native journal rather than matching localized errors.
            // Only a persisted, bounded backoff is eligible for one retry.
            guard let json = try? status(),
                  let state = try? JSONDecoder().decode(AdmissionState.self, from: Data(json.utf8)),
                  state.schema == 1, state.phase == "failed", !state.blocked, !state.held,
                  state.attempts >= 0, state.attempts < 3, let deadline = date(state.retry_at) else { throw initialError }
            let delay = deadline.timeIntervalSinceNow
            guard delay >= -1, delay <= 8.25 else { throw initialError }
            // A monotonic wait cannot be stretched by a wall-clock change.
            // Check stop during this startup-only wait; there is no idle poll.
            // ISO8601DateFormatter may round Go's nanoseconds to milliseconds.
            let end = ProcessInfo.processInfo.systemUptime + max(0, delay) + 0.01
            while true {
                guard isActive(), !Task.isCancelled else { throw CancellationError() }
                let remaining = end - ProcessInfo.processInfo.systemUptime
                if remaining <= 0 { break }
                try await Task.sleep(nanoseconds: UInt64(min(remaining, 0.1) * 1_000_000_000))
            }
            guard isActive(), !Task.isCancelled else { throw CancellationError() }
            return try begin(false)
        }
    }

    private struct AdmissionState: Decodable {
        var schema: Int
        var phase: String
        var attempts: Int
        var blocked: Bool
        var held: Bool
        var retry_at: String
    }

    private static func date(_ value: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let result = formatter.date(from: value) { return result }
        formatter.formatOptions = [.withInternetDateTime]
        return formatter.date(from: value)
    }
}
