import Foundation
import Libbox

private final class RegionalSourceRedirectPolicy: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    let protected: Set<String>
    init(domains: String) {
        protected = Set(domains.split(separator: ",").map { $0.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() })
    }
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        let host = request.url?.host?.trimmingCharacters(in: CharacterSet(charactersIn: ".")).lowercased() ?? ""
        completionHandler(protected.firstIndex(of: host) == nil ? request : nil)
    }
}

/// Go compilation runs on a background task; fetches use Apple's networking
/// stack so generation shares the platform's network/proxy configuration.
/// No source address or response body is placed in an NSError or log.
final class NativeSourceFetcher: NSObject, MobilegenSourceFetcherProtocol, @unchecked Sendable {
    private let session: URLSession
    init(configuration: URLSessionConfiguration = .ephemeral, protectedDomains: String = "") {
        configuration.timeoutIntervalForRequest = 25
        configuration.timeoutIntervalForResource = 30
        let policy = RegionalSourceRedirectPolicy(domains: protectedDomains)
        session = URLSession(configuration: configuration, delegate: policy, delegateQueue: nil)
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
