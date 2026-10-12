import SwiftUI
import MapKit
import SakamotoKit

/// A map draft is committed only with Done. Cancelling never changes the running simulation.
struct LocationMapPicker: View {
    @Environment(\.dismiss) private var dismiss
    let isRoute: Bool
    let initialCenter: LocationWaypoint
    let current: LocationWaypoint?
    let onSave: ([LocationWaypoint]) -> Void
    @State private var points: [LocationWaypoint]
    @State private var center: LocationWaypoint
    @State private var focus: LocationWaypoint?
    @State private var fitRequest = 0
    @State private var query = ""
    @State private var results: [MKMapItem] = []
    @State private var searching = false
    @State private var search: MKLocalSearch?
    @State private var error: String?
    @State private var clearing = false

    init(isRoute: Bool, points: [LocationWaypoint], center: LocationWaypoint, current: LocationWaypoint? = nil, onSave: @escaping ([LocationWaypoint]) -> Void) {
        self.isRoute = isRoute; self.initialCenter = center; self.current = current; self.onSave = onSave
        _points = State(initialValue: points); _center = State(initialValue: center)
    }

    var body: some View {
        NavigationStack {
            GeometryReader { geometry in
            VStack(spacing: 0) {
                HStack {
                    TextField("Search a place or address", text: $query, prompt: Text("Search a place or address").foregroundStyle(.secondary))
                        .submitLabel(.search).onSubmit { searchPlaces() }
                        .autocorrectionDisabled().disabled(searching)
                    if !query.isEmpty && !searching {
                        Button { query = ""; results = []; error = nil } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary).frame(minWidth: 44, minHeight: 44) }.accessibilityLabel("Clear search")
                    }
                    if searching { ProgressView().accessibilityLabel("Searching places") }
                    Button { searchPlaces() } label: { Image(systemName: "magnifyingglass").frame(minWidth: 44, minHeight: 44) }
                        .accessibilityLabel("Search places").disabled(searching || query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }.padding(.horizontal)
                if !results.isEmpty {
                    ScrollView {
                        VStack(spacing: 0) {
                            ForEach(Array(results.enumerated()), id: \.offset) { _, item in
                                Button {
                                    let coordinate = item.placemark.coordinate
                                    let point = LocationWaypoint(latitude: coordinate.latitude, longitude: coordinate.longitude)
                                    choose(point); focus = point; results = []; query = item.name ?? query
                                    UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
                                } label: {
                                    VStack(alignment: .leading, spacing: 4) {
                                        Text(item.name ?? "Place").foregroundStyle(.primary)
                                        if let address = item.placemark.title { Text(address).font(.caption).foregroundStyle(.secondary) }
                                    }.frame(maxWidth: .infinity, minHeight: 44, alignment: .leading).padding(.horizontal).padding(.vertical, 8)
                                }.buttonStyle(.plain)
                                Divider()
                            }
                        }
                    }.frame(maxHeight: min(200, geometry.size.height * 0.25))
                }
                ZStack {
                    LocationNativeMap(points: points, current: current, initialCenter: initialCenter, focus: focus, fitRequest: fitRequest, onTap: choose, onCenter: { center = $0 }, onError: { error = $0 })
                    Image(systemName: "plus").foregroundStyle(.primary).padding(6).background(.regularMaterial, in: Circle())
                        .allowsHitTesting(false).accessibilityHidden(true)
                }.frame(minHeight: 80)
                ScrollView {
                    VStack(alignment: .leading, spacing: 8) {
                        Text(isRoute ? "Tap the map to add stops in travel order." : "Tap the map or search to choose a place.")
                            .font(.subheadline)
                        if let error { Text(error).font(.footnote).foregroundStyle(.red).accessibilityIdentifier("map-error") }
                        if isRoute {
                            Text("\(points.count) / 32 stops · \(String(format: "%.2f", distance / 1000)) km").font(.footnote).foregroundStyle(.secondary)
                            HStack {
                                Button { if !points.isEmpty { points.removeLast(); error = nil } } label: { Label("Undo last stop", systemImage: "arrow.uturn.backward") }.disabled(points.isEmpty)
                                Spacer()
                                Button("Clear", role: .destructive) { clearing = true }.disabled(points.isEmpty)
                            }.frame(minHeight: 44)
                        } else if let point = points.first {
                            Text(String(format: "Selected: %.6f, %.6f", point.latitude, point.longitude)).font(.footnote).foregroundStyle(.secondary)
                        }
                        HStack {
                            Button { choose(center) } label: { Label(isRoute ? "Add map center" : "Choose map center", systemImage: "plus.circle") }
                            Spacer()
                            Button { focus = nil; fitRequest += 1 } label: { Label("Show all", systemImage: "arrow.up.left.and.arrow.down.right") }
                        }.frame(minHeight: 44)
                    }.padding()
                }.frame(maxHeight: min(240, geometry.size.height * 0.35))
                .background(Color(uiColor: .systemBackground))
            }
            }
            .navigationTitle(isRoute ? "Edit route" : "Choose location")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) { Button("Done") { commit() }.disabled(isRoute ? points.count < 2 : points.isEmpty) }
            }
            .confirmationDialog("Clear every route stop?", isPresented: $clearing, titleVisibility: .visible) {
                Button("Clear route", role: .destructive) { points = []; error = nil }
            }
            .onDisappear { search?.cancel(); search = nil }
        }
    }
    private var distance: Double { zip(points, points.dropFirst()).reduce(0) { $0 + $1.0.distance(to: $1.1) } }
    private func choose(_ point: LocationWaypoint) {
        do {
            try VirtualLocationSettings(latitude: point.latitude, longitude: point.longitude).validate()
            if isRoute {
                guard points.count < 32 else { throw TunnelProfile.InvalidProfile("This route has 32 stops. Undo a stop before adding another.") }
                if let previous = points.last {
                    try VirtualLocationRoute(points: [previous, point], durationSeconds: 300).validate()
                }
                points.append(point)
            } else { points = [point] }
            error = nil
        } catch { self.error = error.localizedDescription }
    }
    private func commit() {
        do {
            if isRoute { try VirtualLocationRoute(points: points, durationSeconds: 300).validate() }
            onSave(points); dismiss()
        } catch { self.error = error.localizedDescription }
    }
    private func searchPlaces() {
        let text = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !searching else { return }
        let request = MKLocalSearch.Request(); request.naturalLanguageQuery = text
        request.region = MKCoordinateRegion(center: center.coordinate, latitudinalMeters: 50_000, longitudinalMeters: 50_000)
        let operation = MKLocalSearch(request: request); search = operation; searching = true; error = nil
        operation.start { response, failure in
            guard search === operation else { return }
            searching = false; search = nil
            results = Array((response?.mapItems ?? []).prefix(5))
            if failure != nil { error = "Place search is unavailable. Check your connection and try again, or select on the map." }
            else if results.isEmpty { error = "No places found. Try a city name or a fuller address." }
        }
    }
}

extension LocationWaypoint {
    var coordinate: CLLocationCoordinate2D { CLLocationCoordinate2D(latitude: latitude, longitude: longitude) }
}

/// MKMapView keeps panning independent of diagnostic ticks; only explicit focus requests move the camera.
struct LocationNativeMap: UIViewRepresentable {
    let points: [LocationWaypoint]
    var current: LocationWaypoint? = nil
    let initialCenter: LocationWaypoint
    var focus: LocationWaypoint? = nil
    var fitRequest = 0
    var onTap: ((LocationWaypoint) -> Void)? = nil
    var onCenter: ((LocationWaypoint) -> Void)? = nil
    var onError: ((String) -> Void)? = nil
    func makeCoordinator() -> Coordinator { Coordinator(self) }
    func makeUIView(context: Context) -> MKMapView {
        let map = MKMapView(); map.delegate = context.coordinator
        map.isRotateEnabled = false; map.isPitchEnabled = false
        let tap = UITapGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.tapped(_:)))
        tap.cancelsTouchesInView = false; tap.delegate = context.coordinator; map.addGestureRecognizer(tap)
        map.setRegion(MKCoordinateRegion(center: initialCenter.coordinate, span: MKCoordinateSpan(latitudeDelta: initialCenter.latitude == 0 && initialCenter.longitude == 0 ? 130 : 0.025, longitudeDelta: initialCenter.latitude == 0 && initialCenter.longitude == 0 ? 300 : 0.025)), animated: false)
        return map
    }
    func updateUIView(_ map: MKMapView, context: Context) {
        let coordinator = context.coordinator; coordinator.parent = self
        let pointsChanged = coordinator.points != points
        if pointsChanged {
            coordinator.points = points
            map.removeAnnotations(map.annotations.filter { ($0 as? MKPointAnnotation)?.title != "Simulated position" }); map.removeOverlays(map.overlays)
            for (index, point) in points.enumerated() {
                let annotation = MKPointAnnotation(); annotation.coordinate = point.coordinate
                annotation.title = points.count == 1 ? "Selected location" : "Stop \(index + 1)"
                map.addAnnotation(annotation)
            }
            if points.count > 1 {
                var coordinates = points.map(\.coordinate)
                map.addOverlay(MKGeodesicPolyline(coordinates: &coordinates, count: coordinates.count))
            }
        }
        if coordinator.current != current {
            coordinator.current = current
            if let current {
                let annotation = coordinator.positionAnnotation ?? MKPointAnnotation()
                annotation.coordinate = current.coordinate; annotation.title = "Simulated position"
                if coordinator.positionAnnotation == nil { map.addAnnotation(annotation) }
                coordinator.positionAnnotation = annotation
            } else if let annotation = coordinator.positionAnnotation {
                map.removeAnnotation(annotation); coordinator.positionAnnotation = nil
            }
        }
        if !coordinator.fitted || coordinator.fitRequest != fitRequest || (pointsChanged && onTap == nil) {
            coordinator.fitted = true; coordinator.fitRequest = fitRequest
            let defaultPoint = points == [LocationWaypoint(latitude: 0, longitude: 0)] && current == nil && fitRequest == 0
            if !map.annotations.isEmpty && !defaultPoint { map.showAnnotations(map.annotations, animated: false) }
        }
        if coordinator.focus != focus {
            coordinator.focus = focus
            if let focus { map.setRegion(MKCoordinateRegion(center: focus.coordinate, latitudinalMeters: 1500, longitudinalMeters: 1500), animated: !UIAccessibility.isReduceMotionEnabled) }
        }
    }
    static func dismantleUIView(_ map: MKMapView, coordinator: Coordinator) { map.delegate = nil }
    final class Coordinator: NSObject, MKMapViewDelegate, UIGestureRecognizerDelegate {
        var parent: LocationNativeMap
        var points: [LocationWaypoint] = []
        var current: LocationWaypoint?
        var positionAnnotation: MKPointAnnotation?
        var focus: LocationWaypoint?
        var fitted = false
        var fitRequest = 0
        init(_ parent: LocationNativeMap) { self.parent = parent }
        @objc func tapped(_ gesture: UITapGestureRecognizer) {
            guard let map = gesture.view as? MKMapView else { return }
            let coordinate = map.convert(gesture.location(in: map), toCoordinateFrom: map)
            parent.onTap?(LocationWaypoint(latitude: coordinate.latitude, longitude: coordinate.longitude))
        }
        func gestureRecognizer(_ gesture: UIGestureRecognizer, shouldReceive touch: UITouch) -> Bool {
            var view = touch.view
            while let candidate = view { if candidate is MKAnnotationView || candidate is UIControl { return false }; view = candidate.superview }
            return true
        }
        func mapView(_ map: MKMapView, regionDidChangeAnimated animated: Bool) {
            let point = LocationWaypoint(latitude: map.centerCoordinate.latitude, longitude: map.centerCoordinate.longitude)
            DispatchQueue.main.async { [weak self] in self?.parent.onCenter?(point) }
        }
        func mapView(_ map: MKMapView, didFailLoadingMapWithError error: Error) {
            parent.onError?("Map could not load. Check your connection; place search and advanced coordinates remain available.")
        }
        func mapView(_ map: MKMapView, rendererFor overlay: MKOverlay) -> MKOverlayRenderer {
            let line = MKPolylineRenderer(overlay: overlay); line.strokeColor = .systemBlue; line.lineWidth = 4; return line
        }
        func mapView(_ map: MKMapView, viewFor annotation: MKAnnotation) -> MKAnnotationView? {
            let title = (annotation as? MKPointAnnotation)?.title ?? ""
            if title == "Simulated position" {
                let dot = map.dequeueReusableAnnotationView(withIdentifier: "simulated-position") ?? MKAnnotationView(annotation: annotation, reuseIdentifier: "simulated-position")
                dot.annotation = annotation
                dot.image = UIGraphicsImageRenderer(size: CGSize(width: 20, height: 20)).image { _ in
                    let circle = UIBezierPath(ovalIn: CGRect(x: 1, y: 1, width: 18, height: 18))
                    UIColor.systemGreen.setFill(); circle.fill()
                    UIColor.white.setStroke(); circle.lineWidth = 2; circle.stroke()
                }
                dot.displayPriority = .required; dot.canShowCallout = true; dot.accessibilityLabel = title
                return dot
            }
            let marker = map.dequeueReusableAnnotationView(withIdentifier: "location-stop") as? MKMarkerAnnotationView ?? MKMarkerAnnotationView(annotation: annotation, reuseIdentifier: "location-stop")
            marker.annotation = annotation; marker.canShowCallout = true; marker.displayPriority = .required
            marker.markerTintColor = .systemBlue
            marker.glyphText = title.hasPrefix("Stop ") ? String(title.dropFirst(5)) : nil
            return marker
        }
    }
}
