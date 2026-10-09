import SwiftUI

/// The selected name gets the flexible space; the field label stays on one line.
struct ConfigurationMenu: View {
    @ObservedObject var store: ConfigStore

    var body: some View {
        Menu {
            Picker("Configuration", selection: Binding(
                get: { store.selectedProfileID ?? "" },
                set: { id in
                    do { try store.selectProfile(id) }
                    catch { store.reportProfileError(error) }
                }
            )) {
                Text("Choose configuration").tag("")
                ForEach(store.profiles) { Text($0.name).tag($0.id) }
            }
        } label: {
            HStack(spacing: 8) {
                Text("Configuration").fixedSize(horizontal: true, vertical: false)
                Text(store.selectedProfile?.name ?? "Choose configuration")
                    .lineLimit(1).truncationMode(.middle)
                    .frame(maxWidth: .infinity, alignment: .trailing)
                Image(systemName: "chevron.up.chevron.down").font(.caption)
            }
            .frame(minHeight: 44)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Configuration, \(store.selectedProfile?.name ?? "Choose configuration")")
    }
}
