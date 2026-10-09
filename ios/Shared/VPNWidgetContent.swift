import SwiftUI
import WidgetKit
import SakamotoKit

/// The widget host owns the outer margins. Each family gets its own composition;
/// ViewThatFits drops secondary detail before a larger text size can overflow.
struct VPNWidgetContent: View {
    let snapshot: SystemSurfaceSnapshot
    let date: Date
    let family: WidgetFamily
    private var busy: Bool { snapshot.serviceState == .starting || snapshot.serviceState == .stopping }

    var body: some View {
        Group {
            switch family {
            case .systemSmall: small
            case .systemLarge: large
            default: medium
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
        .widgetURL(URL(string: "sakamoto://home"))
    }

    private var small: some View {
        ViewThatFits(in: .vertical) {
            VStack(alignment: .leading, spacing: 8) {
                brand
                action
                status
            }
            VStack(alignment: .leading, spacing: 6) {
                action
                status
            }
        }
    }

    private var medium: some View {
        HStack(alignment: .center, spacing: 12) {
            ViewThatFits(in: .vertical) {
                VStack(alignment: .leading, spacing: 6) {
                    brand
                    status
                    node
                }
                VStack(alignment: .leading, spacing: 4) {
                    status
                    node
                }
                status
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            VStack(spacing: 6) {
                action
                latency
            }
            .frame(maxWidth: 96)
        }
    }

    private var large: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                brand
                Spacer(minLength: 8)
                action
            }
            status
            node
            HStack(alignment: .firstTextBaseline) {
                Text(snapshot.routingMode.isEmpty ? "Rule" : snapshot.routingMode)
                    .lineLimit(1)
                    .foregroundStyle(.secondary)
                Spacer(minLength: 8)
                latency
            }
            .font(.subheadline)
            Spacer(minLength: 0)
            Text(snapshot.phase == .reachable ? "Last network check passed" : "Open Home to check the network")
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(2)
        }
    }

    private var brand: some View {
        Label("sakamoto", systemImage: "network")
            .font(.headline)
            .lineLimit(1)
            .minimumScaleFactor(0.85)
    }

    private var status: some View {
        Text(statusTitle)
            .font(.subheadline.weight(.semibold))
            .lineLimit(2)
            .fixedSize(horizontal: false, vertical: true)
    }

    private var node: some View {
        Text(snapshot.selectedNode.isEmpty ? "No selected node" : snapshot.selectedNode)
            .font(.caption)
            .foregroundStyle(.secondary)
            .lineLimit(1)
            .truncationMode(.middle)
    }

    private var statusTitle: String {
        switch snapshot.phase {
        case .tunRunning: return "VPN running"
        case .reachable: return "Network verified"
        case .unverified: return "Network unverified"
        default: return snapshot.phase.rawValue
        }
    }

    @ViewBuilder private var latency: some View {
        if let value = snapshot.latencyMS, value > 0 {
            VStack(spacing: 1) {
                Text("\(value) ms").monospacedDigit().lineLimit(1).minimumScaleFactor(0.8)
                if !snapshot.latencyIsFresh(at: date) { Text("Last test").font(.caption2).lineLimit(1) }
            }
            .font(.caption)
            .foregroundStyle(snapshot.latencyIsFresh(at: date) ? .primary : .secondary)
        } else {
            Text("Not tested").font(.caption).foregroundStyle(.secondary).lineLimit(1)
        }
    }

    @ViewBuilder private var action: some View {
        if #available(iOS 17.0, *) {
            Button(intent: ToggleTunnelIntent()) {
                Image(systemName: "power")
                    .font(.title3.weight(.semibold))
                    .frame(width: 44, height: 44)
                    .background(.quaternary, in: Circle())
            }
            .buttonStyle(.plain)
            .disabled(busy)
            .accessibilityLabel(snapshot.serviceState == .running ? "Disconnect VPN" : "Connect VPN")
        } else {
            Link(destination: URL(string: "sakamoto://home")!) {
                Image(systemName: "power").frame(width: 44, height: 44)
            }
            .accessibilityLabel("Open VPN connection")
        }
    }
}
