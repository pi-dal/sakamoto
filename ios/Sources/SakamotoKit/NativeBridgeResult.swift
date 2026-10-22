import Foundation

/// gomobile string/object returns keep NSError as an explicit out parameter;
/// unlike BOOL returns, Swift does not import these methods as throwing calls.
public func checkedNativeValue(_ call: (AutoreleasingUnsafeMutablePointer<NSError?>) -> String) throws -> String {
    var error: NSError?
    let value = call(&error)
    if let error { throw error }
    return value
}
