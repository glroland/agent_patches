import { Router } from 'express';
import * as summaryController from '../controllers/summaryController.js';

const router = Router();

router.get('/', summaryController.getSummary);

export default router;
