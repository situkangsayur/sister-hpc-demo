// Petunjuk yang tampil bila profil compose untuk halaman ini belum dijalankan.
export function Down({ profile, note }: { profile: string; note?: string }) {
  return (
    <div className="card down">
      <h3>Profil <code>{profile}</code> belum berjalan</h3>
      <p>Nyalakan dari folder <code>bab07-replikasi-konsistensi-kubernetes/</code>:</p>
      <pre>docker compose --profile {profile} up -d --build</pre>
      {note && <p className="muted">{note}</p>}
      <p className="muted">Halaman ini memeriksa ulang setiap detik, jadi tidak perlu dimuat ulang.</p>
    </div>
  );
}
