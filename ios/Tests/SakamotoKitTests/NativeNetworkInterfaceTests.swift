import Darwin
import XCTest
@testable import SakamotoKit

final class NativeNetworkInterfaceTests: XCTestCase {
    func testIPv4AndIPv6Masks() {
        XCTAssertEqual(NativeNetworkInterface.prefixLength([255, 255, 255, 0]), 24)
        XCTAssertEqual(NativeNetworkInterface.prefixLength([255, 255, 255, 255]), 32)
        XCTAssertEqual(NativeNetworkInterface.prefixLength([0, 0, 0, 0]), 0)
        XCTAssertEqual(NativeNetworkInterface.prefixLength(Array(repeating: 255, count: 8) + Array(repeating: 0, count: 8)), 64)
        XCTAssertEqual(NativeNetworkInterface.prefixLength([255, 255, 254, 0]), 23)
        XCTAssertNil(NativeNetworkInterface.prefixLength([255, 0, 255, 0]))
        XCTAssertNil(NativeNetworkInterface.prefixLength([255, 127, 0, 0]))
    }

    func testNativeLoopbackHasUsableFlagsMTUAndAddresses() throws {
        let interfaces = try NativeNetworkInterface.read()
        let loopback = try XCTUnwrap(interfaces["lo0"])
        XCTAssertNotEqual(loopback.flags & UInt32(IFF_UP), 0)
        XCTAssertNotEqual(loopback.flags & UInt32(IFF_LOOPBACK), 0)
        XCTAssertGreaterThan(loopback.mtu, 0)
        XCTAssertTrue(loopback.addresses.contains("127.0.0.1/8"))
        XCTAssertTrue(loopback.addresses.contains("::1/128"))
        for interface in interfaces.values {
            XCTAssertEqual(interface.addresses, Array(Set(interface.addresses)).sorted())
            XCTAssertTrue(interface.addresses.allSatisfy { !$0.contains("%") && $0.contains("/") })
        }
    }
}
