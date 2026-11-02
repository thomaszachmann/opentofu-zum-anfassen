# setup/env.sh – zu Beginn jedes Trainingstags einlesen: source ~/tf-lab/setup/env.sh
export LAB="$HOME/tf-lab"
export VM_IP="${VM_IP:-$(hostname -I | awk '{print $1}')}"   # erste IP der VM
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml                  # k3s (ab Tag 6)
export TF_PLUGIN_CACHE_DIR="$HOME/.terraform.d/plugin-cache" # Provider nur einmal laden
mkdir -p "$TF_PLUGIN_CACHE_DIR"
export NOTES_API_KEY="lab-api-key-123"                       # nur Labor
true
