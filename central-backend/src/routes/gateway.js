import { Router } from 'express';
import * as gatewayController from '../controllers/gatewayController.js';

const router = Router();

router.get('/stats', gatewayController.getStats);

export default router;
