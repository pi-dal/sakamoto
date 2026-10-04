import Foundation
import Libbox
import NetworkExtension

// Ported (iOS subset) from SagerNet/sing-box-for-apple, dev branch:
//   Library/Network/Extension+RunBlocking.swift
//   Library/Network/Extension+Iterator.swift
//   Library/Network/ExtensionErrors.swift
// Source commit: d1224bb5081b3df5d0ecc55b1bd3d72ea6c60628 (2026-10-02).
//
// Copyright (C) 2022 by nekohasekai <contact-sagernet@sekai.icu>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.
//
// The sakamoto NOTICE.md file records this derivation as well.

public class ExtensionStartupError: Error {
    let message: String

    public init(_ message: String) {
        self.message = message
    }
}

extension ExtensionStartupError: LocalizedError {
    public var errorDescription: String? {
        message
    }
}

extension ExtensionStartupError: CustomNSError {
    public static var errorDomain: String {
        "ExtensionStartupError"
    }

    public var errorCode: Int {
        1
    }

    public var errorUserInfo: [String: Any] {
        [NSLocalizedDescriptionKey: message]
    }
}

func runBlocking<T>(_ block: @escaping () async -> T) -> T {
    let semaphore = DispatchSemaphore(value: 0)
    let box = resultBox<T>()
    Task.detached(priority: .userInitiated) {
        let value = await block()
        box.result0 = value
        semaphore.signal()
    }
    semaphore.wait()
    return box.result0
}

func runBlocking<T>(_ tBlock: @escaping () async throws -> T) throws -> T {
    let semaphore = DispatchSemaphore(value: 0)
    let box = resultBox<T>()
    Task.detached(priority: .userInitiated) {
        do {
            let value = try await tBlock()
            box.result = .success(value)
        } catch {
            box.result = .failure(error)
        }
        semaphore.signal()
    }
    semaphore.wait()
    return try box.result.get()
}

private class resultBox<T> {
    var result: Result<T, Error>!
    var result0: T!
}

public extension LibboxStringIteratorProtocol {
    func toArray() -> [String] {
        var array: [String] = []
        while hasNext() {
            array.append(next())
        }
        return array
    }
}

public extension LibboxInt32IteratorProtocol {
    func toArray() -> [Int32] {
        var array: [Int32] = []
        while hasNext() {
            array.append(next())
        }
        return array
    }
}

public extension Sequence<String> {
    func toStringIterator() -> LibboxStringIteratorProtocol {
        StringArrayIterator(Array(self))
    }
}

public extension Sequence<Int32> {
    func toInt32Iterator() -> LibboxInt32IteratorProtocol {
        Int32ArrayIterator(Array(self))
    }
}

private final class StringArrayIterator: NSObject, LibboxStringIteratorProtocol {
    private let array: [String]
    private var index: Int = 0
    private var nextValue: String = ""

    init(_ array: [String]) {
        self.array = array
    }

    func len() -> Int32 {
        Int32(array.count - index)
    }

    func hasNext() -> Bool {
        guard index < array.count else { return false }
        nextValue = array[index]
        index += 1
        return true
    }

    func next() -> String {
        nextValue
    }
}

private final class Int32ArrayIterator: NSObject, LibboxInt32IteratorProtocol {
    private let array: [Int32]
    private var index: Int = 0
    private var nextValue: Int32 = 0

    init(_ array: [Int32]) {
        self.array = array
    }

    func len() -> Int32 {
        Int32(array.count - index)
    }

    func hasNext() -> Bool {
        guard index < array.count else { return false }
        nextValue = array[index]
        index += 1
        return true
    }

    func next() -> Int32 {
        nextValue
    }
}
