import Foundation

// Mirror of ios/contract/vocabulary.json.
//
// The golden is generated from internal/core constants by
// pkg/mobilecore/contract_test.go. The Go test keeps the JSON pinned to the
// Go vocabulary; ContractAlignmentTests keeps the Swift enums pinned to the
// same JSON. Any drift anywhere fails the toolchain that drifted.

public struct VocabularyContract: Codable, Equatable, Sendable {
    public var contractVersion: Int
    public var source: String
    public var serviceStates: [String]
    public var sessionPhases: [String]
    public var probeStates: [String]
    public var probePaths: [String]
    public var routingModes: [String]
    public var routingModeCycle: [String]
    public var nodeStatuses: [String]
    public var configStates: [String]
    public var configEvents: [String]
    public var noticeKinds: [String]

    enum CodingKeys: String, CodingKey {
        case contractVersion
        case source
        case serviceStates
        case sessionPhases
        case probeStates
        case probePaths
        case routingModes
        case routingModeCycle
        case nodeStatuses
        case configStates
        case configEvents
        case noticeKinds
    }

    public static func decode(_ data: Data) throws -> VocabularyContract {
        let decoder = JSONDecoder()
        return try decoder.decode(VocabularyContract.self, from: data)
    }
}

public enum VocabularyContractError: Error, Equatable, Sendable {
    /// The golden's contractVersion is newer than this build understands.
    case unsupportedVersion(Int)
}
