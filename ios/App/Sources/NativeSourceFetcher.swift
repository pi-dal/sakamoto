import Foundation
import Libbox

/// Go compilation runs on a background task; fetches use Apple's networking
/// stack so generation shares the platform's network/proxy configuration.
/// No source address or response body is placed in an NSError or log.
final class NativeSourceFetcher: NSObject, MobilegenSourceFetcherProtocol, @unchecked Sendable {
    private let session: URLSession
    init(configuration: URLSessionConfiguration = .ephemeral) {
        configuration.timeoutIntervalForRequest = 25
        configuration.timeoutIntervalForResource = 30
        session = URLSession(configuration: configuration)
        super.init()
    }
    deinit { session.invalidateAndCancel() }

    func fetch(_ source: String?) throws -> MobilegenSourceResponse {
        let result = MobilegenSourceResponse()
        guard let source, let url = URL(string: source), ["http", "https"].contains(url.scheme?.lowercased() ?? "") else {
            result.failure = "network"; return result
        }
        let semaphore = DispatchSemaphore(value: 0)
        var request = URLRequest(url: url)
        request.setValue("sakamoto/0.1 (source generation)", forHTTPHeaderField: "User-Agent")
        let task = session.dataTask(with: request) { data, response, error in
            defer { semaphore.signal() }
            if let error {
                switch (error as? URLError)?.code {
                case .timedOut: result.failure = "timeout"
                case .cannotFindHost, .dnsLookupFailed: result.failure = "dns"
                case .notConnectedToInternet: result.failure = "offline"
                case .secureConnectionFailed, .serverCertificateUntrusted, .serverCertificateHasBadDate, .serverCertificateNotYetValid, .serverCertificateHasUnknownRoot: result.failure = "tls"
                default: result.failure = "network"
                }
            } else if let response = response as? HTTPURLResponse, let data {
                if data.count > 16 << 20 { result.failure = "too-large" }
                else { result.statusCode = Int32(response.statusCode); result.body = data }
            } else { result.failure = "network" }
        }
        task.resume()
        semaphore.wait()
        return result
    }
}
