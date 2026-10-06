import SwiftUI

// About tab: the iOS rendering of the TUI's About / Copyright & attribution
// screens (internal/tui/about.go + NOTICE.md). Same statements, same
// attribution, no binary portrait assets on iOS (the terminal pixel-art is
// a TUI feature; the text claims are the part that must travel).

struct AboutView: View {
    var body: some View {
        List {
            Section {
                VStack(alignment: .leading, spacing: 6) {
                    Text("sakamoto")
                        .font(.title2.bold())
                    Text("A connection-centered client for sing-box — this app is the iOS surface; the macOS TUI is the original.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                .padding(.vertical, 2)
            }

            Section {
                Text("Software © 2026 pi-dal. Code license: GPL-3.0-or-later.")
                Text("This app links to SagerNet/sing-box (via Libbox), also GPL-3.0-or-later. sing-box upstream © nekohasekai and contributors; see NOTICE.md for its license terms and the full attribution.")
                Link("Upstream sing-box", destination: URL(string: "https://github.com/SagerNet/sing-box")!)
                Link("License text (GPL-3.0-or-later)", destination: URL(string: "https://www.gnu.org/licenses/gpl-3.0.html")!)
            } header: {
                Text("License")
            }

            Section {
                Text("The iOS PacketTunnel integration ports the iOS-required subset of the official sing-box-for-apple extension sources (GPL-3.0-or-later, © nekohasekai); each ported file carries the upstream header. The framework binaries are built from the upstream Go module's own builder — the repository is not vendored.")
                Link("sing-box-for-apple", destination: URL(string: "https://github.com/SagerNet/sing-box-for-apple")!)
            } header: {
                Text("sing-box-for-apple port")
            }

            Section {
                Text("The name sakamoto honors musician and composer Ryuichi Sakamoto (1952–2023). This independent project is not affiliated with or endorsed by him, his family, estate, or representatives.")
                Text("About portrait: photo by Joi Ito; Commons crop by Solid State Survivor. Adapted into grayscale terminal pixels, licensed CC BY 2.0. The app icon uses artwork provided by the project owner. See NOTICE.md and docs/portrait-license.md.")
                Link("Source photograph", destination: URL(string: "https://commons.wikimedia.org/wiki/File:RyuichiSakamoto2007.jpg")!)
                Link("CC BY 2.0", destination: URL(string: "https://creativecommons.org/licenses/by/2.0/")!)
            } header: {
                Text("Name & portrait")
            }

            Section {
                Text("sakamoto is an independent open-source project. It is not affiliated with, endorsed by, or an official client of SagerNet, nekohasekai, Tailscale, Shadowrocket, or Apple. Product names belong to their owners and are used only to describe compatibility and provenance.")
            } header: {
                Text("Independent / non-official")
            } footer: {
                Text("Full provenance: NOTICE.md in the repository.")
            }
        }
        .navigationTitle("About")
        .listStyle(.insetGrouped)
    }
}
