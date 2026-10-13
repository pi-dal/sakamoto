# Mobile policy domain matching

To route a domain and all its subdomains together, choose **Domain & all subdomains** and enter `aihubmix.com`.

| Choice | Input | Matches |
| --- | --- | --- |
| Exact domain | `aihubmix.com` | Only `aihubmix.com` |
| Domain & all subdomains | `aihubmix.com` | `aihubmix.com`, `api.aihubmix.com`, `chat.api.aihubmix.com`, and any deeper subdomain |
| Domain contains | `aihubmix` | Any domain containing that text, including names outside `aihubmix.com` |

The subdomain choice uses a domain-label boundary: it does not match `notaihubmix.com` or `aihubmix.com.example`. An HTTP(S) URL uses its host; a URL path is not a routing condition.

On iOS, open **Config → Routing rules → Edit custom overrides**. Add a rule, select its match type, enter its value and choose proxy/direct/reject. Tap an existing rule to edit its scope; the trash control or swipe deletes it with confirmation. Switching an exact rule to the subdomain scope preserves its position and identity. Saved rules persist through app restart; Generate includes them and Apply activates them.

The **From input / prefixes** option retains the existing syntax: `aihubmix.com` is exact, while `*.aihubmix.com` or `suffix:aihubmix.com` includes the root and all subdomains. `domain:`, `keyword:`, IP addresses and `cidr:` remain supported. Explicit match choices convert validated input to portable host policy syntax.

On Android, open **Config → Routing rules** and add or edit a rule. Choose **Domain & all subdomains**, enter `aihubmix.com`, and choose the action and outbound. The suffix field also accepts `*.aihubmix.com` and `suffix:aihubmix.com`, converting them to `domain_suffix: ["aihubmix.com"]` in the saved configuration. Comma-separated domains are supported. Saving preserves unrelated existing predicates, so they continue to restrict that rule. Apply changes or connect to use the saved rule.

Existing exact rules retain their scope until explicitly edited. These changes require a new APK/TestFlight build to reach installed clients.
