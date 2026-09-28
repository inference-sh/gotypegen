
/// Stores a field out of line so a struct can reach itself through a Go
/// pointer cycle (a chat's parent chat) and still be a value type. The box
/// is immutable: assigning a new value replaces it, so copies never share a
/// mutation and the wrapper is Sendable.
@propertyWrapper
public struct Indirect<Value: Sendable>: Sendable {
    private final class Box: Sendable {
        let value: Value
        init(_ value: Value) { self.value = value }
    }

    private var box: Box

    public init(wrappedValue: Value) { box = Box(wrappedValue) }

    public var wrappedValue: Value {
        get { box.value }
        set { box = Box(newValue) }
    }
}

extension Indirect: Decodable where Value: Decodable {
    public init(from decoder: Decoder) throws { self.init(wrappedValue: try Value(from: decoder)) }
}

extension Indirect: Encodable where Value: Encodable {
    public func encode(to encoder: Encoder) throws { try wrappedValue.encode(to: encoder) }
}

// An optional indirect field codes like a plain optional one: a missing or
// null key decodes to nil, and nil is left out when encoding.
extension KeyedDecodingContainer {
    public func decode<T: Decodable & Sendable>(_ type: Indirect<T?>.Type, forKey key: Key) throws -> Indirect<T?> {
        Indirect(wrappedValue: try decodeIfPresent(T.self, forKey: key))
    }
}

extension KeyedEncodingContainer {
    public mutating func encode<T: Encodable & Sendable>(_ value: Indirect<T?>, forKey key: Key) throws {
        try encodeIfPresent(value.wrappedValue, forKey: key)
    }
}
