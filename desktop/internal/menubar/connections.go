package menubar

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/desktop"
)

func (p *Panel) setConnections(visible bool) {
	p.connections = visible
	p.backend.SetConnectionsVisible(visible)
	p.connectionQuery = ""
	p.connectionFilter = ""
	p.connectionList = ui.ListState{}
	p.Refresh()
}

func connectionCount(n int) string {
	text := fmt.Sprint(n)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + "," + text[i:]
	}
	return text
}

func (p *Panel) connectionsView(c *ui.Context) {
	s, t := p.snapshot, c.Theme()
	ui.Column(c).Grow(1).MinHeight(0).Gap(8).Children(func() {
		panelSearchLabel(c.Key("connection-search"), &p.connectionQuery, "Search connections")
		query := strings.ToLower(strings.TrimSpace(p.connectionQuery))
		items := make([]desktop.Connection, 0, len(s.Connections))
		for _, v := range s.Connections {
			if query == "" || strings.Contains(strings.ToLower(strings.Join([]string{v.Target, v.Destination, v.Process, v.Outbound, v.Network}, "\n")), query) {
				items = append(items, v)
			}
		}
		if s.ConnectionsState == desktop.ConnectionsLive {
			count := connectionCount(len(s.Connections)) + " active"
			if query != "" {
				count = connectionCount(len(items)) + " of " + connectionCount(len(s.Connections))
			}
			ui.Text(c, count).FontSize(fontSmall).TextColor(t.TextMuted)
		}
		if p.connectionFilter != p.connectionQuery {
			p.connectionFilter = p.connectionQuery
			p.connectionList.ScrollTo(0, ui.Start)
		}
		p.connectionList.Key = func(i int) any { return items[i].ID }
		p.connectionList.Label = func(i int) string {
			v := items[i]
			return fmt.Sprintf("%s, %s, %s, %s, downloaded %s, uploaded %s", v.Target, v.Destination, v.Process, v.Outbound, formatBytes(v.DownlinkTotal), formatBytes(v.UplinkTotal))
		}
		ui.List(c.Key("connections"), &p.connectionList, len(items), func(i int) { p.connectionRow(c, items[i]) }).Grow(1).MinHeight(90).Children(func() {
			if len(items) == 0 {
				text := "Loading connections…"
				switch {
				case s.Service != core.ServiceRunning:
					text = "Connect to view connections."
					if s.ExternalCore {
						text = "Start the core service to view connections."
					}
				case s.ConnectionsState == desktop.ConnectionsUnsupported:
					text = "Connection tracking unavailable"
				case s.ConnectionsState == desktop.ConnectionsRetrying:
					text = "Reconnecting…"
				case s.ConnectionsState == desktop.ConnectionsLive && query != "":
					text = "No matching connections"
				case s.ConnectionsState == desktop.ConnectionsLive:
					text = "No active connections"
				}
				ui.Text(c, text).FontSize(fontSmall).TextColor(t.TextMuted).Padding(12, 0)
			}
		})
	})
}

func (p *Panel) connectionRow(c *ui.Context, v desktop.Connection) {
	t := c.Theme()
	ui.Row(c.Key(v.ID)).Height(60).AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, v.Target).SingleLine().Label(v.Target + " " + v.Destination)
			parts := []string{}
			if v.Process != "" {
				parts = append(parts, v.Process)
			}
			if v.Outbound != "" {
				parts = append(parts, v.Outbound)
			}
			detail := strings.Join(parts, " → ")
			if v.Network != "" {
				if detail != "" {
					detail += " / "
				}
				detail += strings.ToUpper(v.Network)
			}
			if detail != "" {
				ui.Text(c, detail).FontSize(fontSmall).TextColor(t.TextMuted).SingleLine().Label(detail)
			}
		})
		ui.Column(c).AlignItems(ui.End).Gap(4).Children(func() {
			ui.Text(c, "↓ "+formatBytes(v.DownlinkTotal)).FontSize(fontSmall).Label("Downloaded " + formatBytes(v.DownlinkTotal))
			ui.Text(c, "↑ "+formatBytes(v.UplinkTotal)).FontSize(fontSmall).TextColor(t.TextMuted).Label("Uploaded " + formatBytes(v.UplinkTotal))
		})
	})
}
