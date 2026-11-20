# Repo gitops (desired state)

Isi repo ini adalah *desired state* aplikasi `app` di cluster k3s. Argo CD mengawasi folder
`app/` dan menyamakan cluster dengan isinya. Jenkins hanya mengubah satu baris: `newTag` di
`app/kustomization.yaml`. Rollback = commit yang mengembalikan `newTag` ke tag lama.
