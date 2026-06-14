// Placeholder handler for routes that are scaffolded but not yet implemented.
export function notImplemented(req, res) {
  res.status(501).json({
    error: 'not_implemented',
    message: `${req.method} ${req.route?.path ?? req.path} is scaffolded but not yet implemented`,
  });
}
