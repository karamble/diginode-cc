export default function MapPage() {
  return (
    <div className="h-full flex flex-col">
      <div className="p-4 border-b border-dark-700">
        <h2 className="text-lg font-semibold text-dark-100">Map</h2>
      </div>
      <div className="flex-1 bg-dark-900 flex items-center justify-center">
        <p className="text-dark-400">Leaflet map will render here</p>
      </div>
    </div>
  )
}
