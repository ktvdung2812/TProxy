export type LanPortOsTab = "windows" | "macos" | "linux";

export type LanPortCommandSection = {
  title: string;
  language: string;
  command: string;
  note?: string;
};

export const LAN_PORT_OS_TABS: { id: LanPortOsTab; label: string }[] = [
  { id: "windows", label: "Windows" },
  { id: "macos", label: "macOS" },
  { id: "linux", label: "Linux" },
];

export function lanPortCommandSections(port: number, os: LanPortOsTab): LanPortCommandSection[] {
  switch (os) {
    case "windows":
      return [
        {
          title: "PowerShell (Administrator)",
          language: "powershell",
          command: [
            `New-NetFirewallRule -DisplayName "tproxy LAN (TCP ${port})" ` +
              "-Direction Inbound -Protocol TCP " +
              `-LocalPort ${port} -Action Allow`,
          ].join(""),
          note: "Run in an elevated PowerShell session.",
        },
        {
          title: "Command Prompt (Administrator)",
          language: "bat",
          command:
            `netsh advfirewall firewall add rule name="tproxy LAN (TCP ${port})" ` +
            `dir=in action=allow protocol=TCP localport=${port}`,
        },
      ];
    case "macos":
      return [
        {
          title: "Packet Filter (temporary until reboot)",
          language: "bash",
          command: `echo "pass in proto tcp from any to any port ${port}" | sudo pfctl -ef -`,
          note: "Requires sudo. For a persistent rule, add the pass line to /etc/pf.conf and reload pf.",
        },
        {
          title: "Application Firewall (allow the tproxy binary)",
          language: "bash",
          command: [
            "sudo /usr/libexec/ApplicationFirewall/socketfilterfw --setglobalstate on",
            "sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add /path/to/tproxy",
            "sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp /path/to/tproxy",
          ].join("\n"),
          note: "Replace /path/to/tproxy with the actual binary path.",
        },
      ];
    case "linux":
      return [
        {
          title: "UFW (Ubuntu / Debian)",
          language: "bash",
          command: [`sudo ufw allow ${port}/tcp comment 'tproxy LAN'`, "sudo ufw reload"].join("\n"),
        },
        {
          title: "firewalld (Fedora / RHEL)",
          language: "bash",
          command: [
            `sudo firewall-cmd --permanent --add-port=${port}/tcp`,
            "sudo firewall-cmd --reload",
          ].join("\n"),
        },
        {
          title: "iptables",
          language: "bash",
          command: `sudo iptables -I INPUT -p tcp --dport ${port} -j ACCEPT`,
          note: "Persist the rule with your distro's iptables save mechanism (e.g. iptables-save).",
        },
      ];
  }
}
