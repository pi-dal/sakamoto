import SwiftUI
#if DEBUG && SAKAMOTO_SIM_AGENTATION && targetEnvironment(simulator)
import SimAgentationPlus
#endif

/// Instrument the shared UI without adding a package dependency or raising
/// deployment targets in the distribution project. The SDK is linked only
/// by project.sim-agentation.yml and runs only in Debug simulators.
extension View {
    @ViewBuilder
    func sakamotoInspector() -> some View {
        #if DEBUG && SAKAMOTO_SIM_AGENTATION && targetEnvironment(simulator)
        self.simAgentation()
        #else
        self
        #endif
    }

    @ViewBuilder
    func sakamotoInspectTag(_ name: String, fileID: String = #fileID, line: Int = #line) -> some View {
        #if DEBUG && SAKAMOTO_SIM_AGENTATION && targetEnvironment(simulator)
        self.simTag(name, fileID: fileID, line: line)
        #else
        self
        #endif
    }
}
